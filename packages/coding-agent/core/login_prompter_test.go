package core

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/guanshan/pi-go/packages/ai"
)

// interactiveLoop drives an interactiveModel the way bubbletea's program loop
// does: a single goroutine owns the model and is the ONLY caller of Update, so
// posted messages (model.post) and injected key presses are serialized onto one
// goroutine. Tests must interact only through send/query — never read or mutate
// model fields from another goroutine — which mirrors production (model.post is
// program.Send, and Update only ever runs on the event-loop goroutine). The
// /login prompter blocks on its own runSlashCommand goroutine and unblocks when
// a key press routed through this loop resolves the overlay channel.
type interactiveLoop struct {
	model *interactiveModel
	msgs  chan tea.Msg
	quit  chan struct{}
}

type interactiveQueryMsg struct {
	fn   func(*interactiveModel)
	done chan struct{}
}

func startInteractiveLoop(model *interactiveModel) *interactiveLoop {
	l := &interactiveLoop{
		model: model,
		msgs:  make(chan tea.Msg, 256),
		quit:  make(chan struct{}),
	}
	model.post = func(msg tea.Msg) {
		select {
		case l.msgs <- msg:
		case <-l.quit:
		}
	}
	go func() {
		for {
			select {
			case msg := <-l.msgs:
				if q, ok := msg.(interactiveQueryMsg); ok {
					q.fn(model)
					close(q.done)
					continue
				}
				model.Update(msg)
			case <-l.quit:
				return
			}
		}
	}()
	return l
}

// send injects a message (e.g. a key press) onto the loop goroutine.
func (l *interactiveLoop) send(msg tea.Msg) {
	select {
	case l.msgs <- msg:
	case <-l.quit:
	}
}

// query runs fn on the loop goroutine (the model's owner) and waits for it,
// so reads of model state never race with Update.
func (l *interactiveLoop) query(fn func(*interactiveModel)) {
	done := make(chan struct{})
	select {
	case l.msgs <- interactiveQueryMsg{fn: fn, done: done}:
		<-done
	case <-l.quit:
	}
}

func (l *interactiveLoop) stop() { close(l.quit) }

// waitForOverlay blocks until an extension-UI overlay request is active (read on
// the loop goroutine) or the deadline elapses.
func (l *interactiveLoop) waitForOverlay(t *testing.T) *interactiveExtensionUIRequest {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		var req *interactiveExtensionUIRequest
		l.query(func(m *interactiveModel) { req = m.extensionUI })
		if req != nil {
			return req
		}
		select {
		case <-deadline:
			t.Fatal("login prompter did not post an input overlay request")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestInteractiveLoginPrompterDrivesOAuthThroughOverlay verifies the P0 fix: the
// interactive TUI now passes a real prompter (not nil) for /login, so an OAuth
// provider that calls OnPrompt receives the value the user types into the
// extension-UI input overlay, and the resulting credentials are saved.
func TestInteractiveLoginPrompterDrivesOAuthThroughOverlay(t *testing.T) {
	ai.RegisterOAuthProvider(ai.OAuthProvider{
		ProviderID:   "tui-oauth",
		ProviderName: "TUI OAuth",
		LoginFunc: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
			code, err := callbacks.OnPrompt(ai.OAuthPrompt{Message: "Paste the auth code"})
			if err != nil {
				return ai.OAuthCredentials{}, err
			}
			return ai.OAuthCredentials{
				Access:  "token-" + code,
				Refresh: "refresh",
				Expires: time.Now().Add(time.Hour).UnixMilli(),
			}, nil
		},
	})
	defer ai.UnregisterOAuthProvider("tui-oauth")

	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	loop := startInteractiveLoop(model)
	defer loop.stop()

	// runSlashCommand returns a tea.Cmd that runs the slash handler (and thus the
	// blocking OAuth prompter) on its own goroutine, exactly like the program loop.
	cmd := model.runSlashCommand(context.Background(), "/login tui-oauth --oauth")
	doneMsg := make(chan tea.Msg, 1)
	go func() { doneMsg <- cmd() }()

	req := loop.waitForOverlay(t)
	if req.Method != "input" {
		t.Fatalf("expected input overlay, got %q", req.Method)
	}

	// Type a code and submit; handleExtensionUIKey resolves the overlay channel.
	for _, r := range "abc123" {
		loop.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	loop.send(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case msg := <-doneMsg:
		done, ok := msg.(interactiveCommandDoneMsg)
		if !ok {
			t.Fatalf("expected interactiveCommandDoneMsg, got %T", msg)
		}
		if done.Err != nil {
			t.Fatalf("login command error: %v", done.Err)
		}
		if !strings.Contains(done.Stdout, "Saved OAuth credentials for tui-oauth") {
			t.Fatalf("login output missing saved-credentials line: %q", done.Stdout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login command did not finish after overlay was answered")
	}

	agent := runtime.Session()
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "tui-oauth"}); got != "token-abc123" {
		t.Fatalf("oauth access token=%q, want token-abc123 (value should flow from the overlay)", got)
	}
}

// TestInteractiveLoginPrompterCancelAborts verifies esc/ctrl+c during the OAuth
// prompt resolves the overlay with a null, which the prompter turns into a
// cancellation error so the login command cannot hang.
func TestInteractiveLoginPrompterCancelAborts(t *testing.T) {
	ai.RegisterOAuthProvider(ai.OAuthProvider{
		ProviderID:   "tui-oauth-cancel",
		ProviderName: "TUI OAuth Cancel",
		LoginFunc: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
			_, err := callbacks.OnPrompt(ai.OAuthPrompt{Message: "Paste the auth code"})
			return ai.OAuthCredentials{}, err
		},
	})
	defer ai.UnregisterOAuthProvider("tui-oauth-cancel")

	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	loop := startInteractiveLoop(model)
	defer loop.stop()

	cmd := model.runSlashCommand(context.Background(), "/login tui-oauth-cancel --oauth")
	doneMsg := make(chan tea.Msg, 1)
	go func() { doneMsg <- cmd() }()

	loop.waitForOverlay(t)
	// Cancel the overlay.
	loop.send(tea.KeyPressMsg{Code: tea.KeyEscape})

	select {
	case msg := <-doneMsg:
		done, ok := msg.(interactiveCommandDoneMsg)
		if !ok {
			t.Fatalf("expected interactiveCommandDoneMsg, got %T", msg)
		}
		if done.Err == nil {
			t.Fatal("cancelled login should surface an error, not hang or succeed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled login command hung instead of returning")
	}
}

// TestInteractiveLoginPrompterOnlyForLogin verifies the prompter gate: a
// non-login slash command keeps a nil prompter so it stays non-blocking.
func TestInteractiveLoginPrompterOnlyForLogin(t *testing.T) {
	if isLoginCommand("/model anthropic/claude") {
		t.Fatal("/model must not be treated as a login command")
	}
	if !isLoginCommand("/login anthropic") {
		t.Fatal("/login should be detected")
	}
	if !isLoginCommand("  /login  ") {
		t.Fatal("/login with surrounding whitespace should be detected")
	}
}
