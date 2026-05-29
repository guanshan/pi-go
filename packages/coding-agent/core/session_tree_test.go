package core

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestFormatSessionTreeNavigateAndClone(t *testing.T) {
	sessionDir := t.TempDir()
	session, err := NewSessionManager(t.TempDir(), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(ai.NewUserMessage("first", nil)); err != nil {
		t.Fatal(err)
	}
	firstID := session.Entries[len(session.Entries)-1].ID
	if err := session.AppendMessage(ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("answer"), ai.Usage{}, "stop")); err != nil {
		t.Fatal(err)
	}
	assistantID := session.Entries[len(session.Entries)-1].ID
	if err := session.AppendMessage(ai.NewUserMessage("second", nil)); err != nil {
		t.Fatal(err)
	}

	tree := FormatSessionTree(session)
	for _, want := range []string{"Session tree:", firstID, assistantID, "user", "assistant", "*"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("tree missing %q:\n%s", want, tree)
		}
	}

	if err := session.SetLeaf(assistantID[:6]); err != nil {
		t.Fatal(err)
	}
	if session.CurrentID == nil || *session.CurrentID != assistantID {
		t.Fatalf("current id = %#v, want %s", session.CurrentID, assistantID)
	}

	cloned, err := CloneSessionBranch(session, "", sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Header.ParentSession != session.File() {
		t.Fatalf("parent session = %q, want %q", cloned.Header.ParentSession, session.File())
	}
	ctx := cloned.BuildContext()
	if len(ctx.Messages) != 2 {
		t.Fatalf("cloned messages = %d, want 2", len(ctx.Messages))
	}
	if text := ai.MessageText(ctx.Messages[1]); text != "answer" {
		t.Fatalf("cloned assistant text = %q", text)
	}
}

func TestHandleSlashTreeForkAndClone(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	session, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := session.AppendMessage(ai.NewUserMessage("first", nil)); err != nil {
		t.Fatal(err)
	}
	firstID := session.Entries[len(session.Entries)-1].ID
	if err := session.AppendMessage(ai.NewAssistantMessage("faux", "faux", "faux", ai.TextBlocks("answer"), ai.Usage{}, "stop")); err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       t.TempDir(),
		SessionManager: session,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := handleSlash(context.Background(), runtime, "/tree", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Session tree:") || !strings.Contains(stdout.String(), firstID) {
		t.Fatalf("/tree output = %q", stdout.String())
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), runtime, "/tree "+firstID, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if runtime.Session().Session.CurrentID == nil || *runtime.Session().Session.CurrentID != firstID {
		t.Fatalf("tree navigation current id = %#v", runtime.Session().Session.CurrentID)
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), runtime, "/new", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Started new session:") {
		t.Fatalf("/new output = %q", stdout.String())
	}
	if got := len(runtime.Session().Session.BuildContext().Messages); got != 0 {
		t.Fatalf("new session message count = %d, want 0", got)
	}
	if err := runtime.Session().Session.AppendMessage(ai.NewUserMessage("first", nil)); err != nil {
		t.Fatal(err)
	}
	firstID = runtime.Session().Session.Entries[len(runtime.Session().Session.Entries)-1].ID

	stdout.Reset()
	if _, err := handleSlash(context.Background(), runtime, "/fork "+firstID, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Forked new session:") {
		t.Fatalf("/fork output = %q", stdout.String())
	}
	if runtime.Session().Session.Header.ParentSession == "" {
		t.Fatal("forked session missing parent session")
	}
	if got := len(runtime.Session().Session.BuildContext().Messages); got != 1 {
		t.Fatalf("forked message count = %d, want 1", got)
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), runtime, "/clone", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cloned to new session:") {
		t.Fatalf("/clone output = %q", stdout.String())
	}
}

func TestHandleSlashResumeAndImport(t *testing.T) {
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	initial, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.AppendMessage(ai.NewUserMessage("current", nil)); err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), testRuntimeFactory(t), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       t.TempDir(),
		SessionManager: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	resumed, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.AppendMessage(ai.NewUserMessage("resumed", nil)); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	resumeArg := resumed.File()
	if _, err := handleSlash(context.Background(), runtime, "/resume "+resumeArg, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Resumed session:") {
		t.Fatalf("/resume output = %q", stdout.String())
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "resumed" {
		t.Fatalf("resumed text=%q", got)
	}

	importSource, err := NewSessionManager(filepath.Join(cwd, "import"), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := importSource.AppendMessage(ai.NewUserMessage("imported", nil)); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), runtime, "/import "+importSource.File(), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Imported session:") {
		t.Fatalf("/import output = %q", stdout.String())
	}
	if got := ai.MessageText(runtime.Session().Session.BuildContext().Messages[0]); got != "imported" {
		t.Fatalf("imported text=%q", got)
	}
	if runtime.Session().Session.File() == importSource.File() {
		t.Fatalf("expected imported session to be copied, got same file %q", runtime.Session().Session.File())
	}
}
