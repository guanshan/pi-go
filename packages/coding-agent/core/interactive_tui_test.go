package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestSlashCommandSuggestions(t *testing.T) {
	suggestions := slashCommandSuggestions("/mo")
	if !slices.Contains(suggestions, "/model") {
		t.Fatalf("expected /model suggestion, got %#v", suggestions)
	}
	if got := slashCommandSuggestions("/model gpt"); len(got) != 0 {
		t.Fatalf("expected no suggestions after arguments, got %#v", got)
	}
}

func TestInteractiveBusySubmitQueuesInsteadOfDroppingInput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		text     string
		behavior StreamingBehavior
		followUp bool
	}{
		{name: "enter steers", text: "please adjust"},
		{name: "alt enter follows up", text: "next turn", behavior: StreamingFollowUp, followUp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := testInteractiveRuntime(t)
			model, err := newInteractiveModel(context.Background(), runtime, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			model.busy = true
			model.busyKind = interactiveBusyAgent
			model.input.SetValue(tc.text)
			cmd := model.submitInputWithBehavior(tc.behavior)
			if cmd == nil || cmd() != (interactiveQueueDoneMsg{}) {
				t.Fatalf("queue cmd failed: %#v", cmd)
			}
			if got := strings.TrimSpace(model.input.Value()); got != "" {
				t.Fatalf("input not cleared: %q", got)
			}
			agent := runtime.Session()
			if tc.followUp && (len(agent.followUpQueue) != 1 || agent.followUpQueue[0].Message != tc.text) {
				t.Fatalf("followUpQueue=%#v", agent.followUpQueue)
			}
			if !tc.followUp && (len(agent.steeringQueue) != 1 || agent.steeringQueue[0].Message != tc.text) {
				t.Fatalf("steeringQueue=%#v", agent.steeringQueue)
			}
		})
	}
}

func TestInteractiveCtrlCClearsThenQuits(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("draft")
	if cmd := model.handleCtrlC(); cmd != nil {
		t.Fatalf("first ctrl+c returned cmd %#v", cmd)
	}
	if got := strings.TrimSpace(model.input.Value()); got != "" {
		t.Fatalf("input not cleared: %q", got)
	}
	cmd := model.handleCtrlC()
	if cmd == nil {
		t.Fatal("second ctrl+c should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("second ctrl+c did not quit")
	}
}

func TestInteractiveEscapeCancelsRunningCommand(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.busy = true
	model.busyKind = interactiveBusyCommand
	cmdCtx := model.beginCommand()
	if model.commandCancel == nil {
		t.Fatal("beginCommand did not record a cancel func")
	}
	if cmd := model.handleEscape(); cmd != nil {
		t.Fatalf("escape on a busy command should not return a cmd, got %#v", cmd)
	}
	select {
	case <-cmdCtx.Done():
	default:
		t.Fatal("escape did not cancel the running command context")
	}
	if model.commandCancel != nil {
		t.Fatal("escape did not clear commandCancel")
	}
}

func TestInteractiveBashSubmitIsCancellable(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("!sleep 30")
	cmd := model.submitInputWithBehavior(StreamingSteer)
	if cmd == nil {
		t.Fatal("bash submit returned nil cmd")
	}
	if model.busyKind != interactiveBusyCommand {
		t.Fatalf("busyKind = %q, want command", model.busyKind)
	}
	if model.commandCancel == nil {
		t.Fatal("bash submit did not set a per-command cancel func")
	}
	// Escape must cancel the command without executing the queued cmd closure.
	if escCmd := model.handleEscape(); escCmd != nil {
		t.Fatalf("escape returned cmd %#v", escCmd)
	}
	if model.commandCancel != nil {
		t.Fatal("escape did not clear commandCancel after cancelling bash command")
	}
}

func TestInteractiveModelAndResourceSuggestions(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	agent := runtime.Session()
	agent.Resources.PromptTemplates["review"] = PromptTemplate{Name: "review", Content: "review prompt"}
	agent.Resources.Skills["go"] = Skill{Name: "go", Content: "skill"}
	agent.SetScopedModels([]ScopedModel{{Model: ai.Model{Provider: "openai", ID: "zz-test"}}})

	suggestions := interactiveSuggestions("/re", agent)
	if !slices.Contains(suggestions, "/review") {
		t.Fatalf("prompt template suggestion missing: %#v", suggestions)
	}
	suggestions = interactiveSuggestions("/skill:g", agent)
	if !slices.Contains(suggestions, "/skill:go") {
		t.Fatalf("skill suggestion missing: %#v", suggestions)
	}
	suggestions = interactiveSuggestions("/model zz", agent)
	if !slices.Contains(suggestions, "/model openai/zz-test") {
		t.Fatalf("model suggestion missing: %#v", suggestions)
	}
}

func TestInteractiveInputHistoryNavigation(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.Focus() // so a mis-gated key would actually reach/mutate the textarea
	model.addToHistory("first")
	model.addToHistory("second")
	model.addToHistory("second") // consecutive duplicate is ignored
	model.addToHistory("")       // empty is ignored
	if len(model.history) != 2 || model.history[0] != "second" || model.history[1] != "first" {
		t.Fatalf("history=%#v want [second first]", model.history)
	}

	up := tea.KeyPressMsg{Code: tea.KeyUp}
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	send := func(msg tea.Msg) { model.Update(msg) }

	// Up from an empty editor loads the most recent entry, then older.
	send(up)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("after up#1 value=%q want second", got)
	}
	send(up)
	if got := model.input.Value(); got != "first" {
		t.Fatalf("after up#2 value=%q want first", got)
	}
	// Up past the oldest entry is a no-op.
	send(up)
	if got := model.input.Value(); got != "first" {
		t.Fatalf("after up#3 value=%q want first (clamped)", got)
	}
	// Down walks back toward newest, then to an empty editor.
	send(down)
	if got := model.input.Value(); got != "second" {
		t.Fatalf("after down#1 value=%q want second", got)
	}
	send(down)
	if got := model.input.Value(); got != "" {
		t.Fatalf("after down#2 value=%q want empty", got)
	}
	if model.historyIndex != -1 {
		t.Fatalf("historyIndex=%d want -1 after returning to current", model.historyIndex)
	}
}

func TestInteractiveInputHistoryCapAndEditExit(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 130 {
		model.addToHistory(strings.Repeat("x", i+1)) // all distinct, so none skipped
	}
	if len(model.history) != 100 {
		t.Fatalf("history len=%d want 100 (capped)", len(model.history))
	}

	// Browsing, then typing a character exits history-browsing mode. The textarea
	// only edits when focused (Init() focuses it in the real program).
	model.input.Focus()
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if model.historyIndex != 0 {
		t.Fatalf("historyIndex=%d want 0 after up", model.historyIndex)
	}
	model.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if model.historyIndex != -1 {
		t.Fatalf("historyIndex=%d want -1 after an edit", model.historyIndex)
	}
}

func TestFileReferenceSuggestions(t *testing.T) {
	cwd := t.TempDir()
	for _, name := range []string{"main.go", "README.md", ".hidden", "with space.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cwd, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", "foo.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	all := fileReferenceSuggestions("@", cwd)
	for _, want := range []string{"@README.md", "@main.go", "@src/", "@\"with space.txt\""} {
		if !slices.Contains(all, want) {
			t.Fatalf("@ suggestions %#v missing %q", all, want)
		}
	}
	if slices.Contains(all, "@.hidden") {
		t.Fatalf("@ suggestions should hide top-level dotfiles: %#v", all)
	}

	if got := fileReferenceSuggestions("@ma", cwd); !slices.Contains(got, "@main.go") || slices.Contains(got, "@README.md") {
		t.Fatalf("@ma suggestions=%#v", got)
	}
	if got := fileReferenceSuggestions("@src/", cwd); !slices.Contains(got, "@src/foo.go") {
		t.Fatalf("@src/ suggestions=%#v", got)
	}
	if got := fileReferenceSuggestions("@src/f", cwd); !slices.Contains(got, "@src/foo.go") {
		t.Fatalf("@src/f suggestions=%#v", got)
	}
	// Dotfiles are surfaced once the user descends into a directory (#12).
	if got := fileReferenceSuggestions("@src/", cwd); !slices.Contains(got, "@src/.env") {
		t.Fatalf("@src/ should surface dotfiles in a subdir: %#v", got)
	}

	// Absolute @-references read the real directory, not cwd, and emit absolute
	// completions (#2). Using cwd's own absolute path exercises the absolute branch.
	absToken := "@" + filepath.Join(cwd, "ma")
	if got := fileReferenceSuggestions(absToken, cwd); !slices.Contains(got, "@"+filepath.Join(cwd, "main.go")) {
		t.Fatalf("absolute @ suggestions for %q = %#v", absToken, got)
	}

	// A quoted @-reference may contain spaces; the whole token (from the @) is
	// recovered and the completion is re-quoted (#5).
	token, start, ok := trailingFileRefToken("explain @\"with sp")
	if !ok || start != len("explain ") || token != "@\"with sp" {
		t.Fatalf("trailingFileRefToken quoted: token=%q start=%d ok=%v", token, start, ok)
	}
	if got := fileReferenceSuggestions("@\"with sp", cwd); !slices.Contains(got, "@\"with space.txt\"") {
		t.Fatalf("quoted @ suggestions=%#v want @\"with space.txt\"", got)
	}
}

func TestInteractiveAtCompletionReplacesTrailingToken(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	cwd := runtime.Session().Session.CWD()
	if err := os.WriteFile(filepath.Join(cwd, "notes.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The @-token is completed in place; surrounding text is preserved and a file
	// completion appends a trailing space.
	model.input.SetValue("explain @notes")
	if !model.completeSlashCommand() {
		t.Fatal("expected a completion for @notes")
	}
	if got := model.input.Value(); got != "explain @notes.md " {
		t.Fatalf("completed value=%q want %q", got, "explain @notes.md ")
	}
}

func TestLineExtensionUIHandlerAnswersPrompts(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("y\n2\nhello\n"))
	var stdout bytes.Buffer
	handler := newLineExtensionUIHandler(scanner, &stdout)

	result, err := handler(context.Background(), "confirm", json.RawMessage(`{"message":"Run?"}`))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if string(result) != "true" {
		t.Fatalf("confirm result=%s want true", result)
	}

	result, err = handler(context.Background(), "select", json.RawMessage(`{"message":"Pick","choices":["a","b"]}`))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if string(result) != `"b"` {
		t.Fatalf("select result=%s want %q", result, `"b"`)
	}

	result, err = handler(context.Background(), "input", json.RawMessage(`{"message":"Name"}`))
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if string(result) != `"hello"` {
		t.Fatalf("input result=%s want %q", result, `"hello"`)
	}
}

func TestInteractiveExtensionUIConfirmBindsRuntimeHandler(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	api := coreext.NewAPI()
	runtime.Session().extensionRuntime = coreext.NewRunnerWithAPI(api)

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	posted := make(chan struct{}, 1)
	model.post = func(msg tea.Msg) {
		model.Update(msg)
		posted <- struct{}{}
	}
	model.bindExtensionUIHandler()
	handler := api.UIHandler()
	if handler == nil {
		t.Fatal("expected interactive model to bind extension UI handler")
	}

	type response struct {
		result json.RawMessage
		err    error
	}
	done := make(chan response, 1)
	go func() {
		result, err := handler(context.Background(), "confirm", json.RawMessage(`{"message":"Continue?"}`))
		done <- response{result: result, err: err}
	}()

	select {
	case <-posted:
	case <-time.After(time.Second):
		t.Fatal("extension UI request was not posted to the TUI")
	}
	if model.extensionUI == nil || model.extensionUI.Method != "confirm" {
		t.Fatalf("extensionUI=%#v, want active confirm request", model.extensionUI)
	}
	model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("handler error: %v", got.err)
		}
		if string(got.result) != "true" {
			t.Fatalf("handler result=%s want true", got.result)
		}
	case <-time.After(time.Second):
		t.Fatal("extension UI handler did not receive the confirm response")
	}
}

func testInteractiveRuntime(t *testing.T) *AgentSessionRuntime {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	session := NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
	return &AgentSessionRuntime{
		session: session,
		services: &AgentSessionServices{
			Cwd:             cwd,
			AgentDir:        settings.AgentDir,
			SettingsManager: settings,
			ModelRegistry:   registry,
			ResourceLoader:  resources,
		},
	}
}
