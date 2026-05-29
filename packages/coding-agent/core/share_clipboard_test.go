package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestGetLastAssistantTextSkipsEmptyAbortedMessages(t *testing.T) {
	session := InMemorySession(t.TempDir())
	if err := session.AppendMessage(ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{
		{Type: "thinking", Thinking: "hidden"},
		{Type: "text", Text: "first "},
		{Type: "text", Text: "answer"},
	}, ai.Usage{}, "stop")); err != nil {
		t.Fatal(err)
	}
	aborted := ai.NewAssistantMessage("faux", "faux", "faux", nil, ai.Usage{}, "aborted")
	if err := session.AppendMessage(aborted); err != nil {
		t.Fatal(err)
	}
	agent := &AgentSession{Session: session}
	if got := agent.GetLastAssistantText(); got != "first answer" {
		t.Fatalf("last assistant text = %q", got)
	}
}

func TestCopyTextToClipboardFallsBackToOSC52ForRemoteSession(t *testing.T) {
	restore := replaceProcessHooks(t)
	defer restore()
	goosValue = func() string { return "linux" }
	envValue = func(key string) string {
		if key == "SSH_CONNECTION" {
			return "host"
		}
		return ""
	}
	externalCommand = func(context.Context, string, []string, string) (commandResult, error) {
		t.Fatal("clipboard command should not run without a local display")
		return commandResult{}, nil
	}

	var out bytes.Buffer
	if err := CopyTextToClipboard("copy me", &out); err != nil {
		t.Fatal(err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("copy me")) + "\a"
	if out.String() != want {
		t.Fatalf("OSC52 output = %q, want %q", out.String(), want)
	}
}

func TestShareSessionHTMLCreatesSecretGist(t *testing.T) {
	restore := replaceProcessHooks(t)
	defer restore()

	dir := t.TempDir()
	session, err := NewSessionManager(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}

	externalLookPath = func(file string) (string, error) {
		if file != "gh" {
			t.Fatalf("look path = %q, want gh", file)
		}
		return "/usr/bin/gh", nil
	}
	envValue = func(key string) string {
		if key == shareViewerURLEnvironment {
			return "https://viewer.example/s"
		}
		return ""
	}
	var sawGistCreate bool
	externalCommand = func(_ context.Context, name string, args []string, input string) (commandResult, error) {
		if input != "" {
			t.Fatalf("unexpected stdin for %s: %q", name, input)
		}
		if name != "gh" {
			t.Fatalf("command = %q, want gh", name)
		}
		switch strings.Join(args, " ") {
		case "auth status":
			return commandResult{ExitCode: 0}, nil
		default:
			if len(args) != 4 || args[0] != "gist" || args[1] != "create" || args[2] != "--public=false" {
				t.Fatalf("unexpected gh args: %#v", args)
			}
			html, err := os.ReadFile(args[3])
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(html, []byte(`id="session-data" type="application/json"`)) {
				t.Fatal("shared HTML did not include embedded session data")
			}
			sawGistCreate = true
			return commandResult{Stdout: "https://gist.github.com/user/gist123\n", ExitCode: 0}, nil
		}
	}

	result, err := ShareSessionHTML(context.Background(), session.File())
	if err != nil {
		t.Fatal(err)
	}
	if !sawGistCreate {
		t.Fatal("gh gist create was not called")
	}
	if result.GistID != "gist123" || result.GistURL != "https://gist.github.com/user/gist123" {
		t.Fatalf("unexpected share result: %#v", result)
	}
	if result.PreviewURL != "https://viewer.example/s/gist123" {
		t.Fatalf("preview URL = %q", result.PreviewURL)
	}
}

func TestHandleSlashCopyCopiesLastAssistantMessage(t *testing.T) {
	restore := replaceProcessHooks(t)
	defer restore()
	goosValue = func() string { return "linux" }
	envValue = func(key string) string {
		if key == "SSH_CONNECTION" {
			return "host"
		}
		return ""
	}
	externalCommand = func(context.Context, string, []string, string) (commandResult, error) {
		return commandResult{ExitCode: 1}, nil
	}

	agent := &AgentSession{Session: InMemorySession(t.TempDir())}
	msg := ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{{Type: "text", Text: "last reply"}}, ai.Usage{}, "stop")
	if err := agent.Session.AppendMessage(msg); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done, err := handleSlash(context.Background(), agent, "/copy", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("/copy should not end the interactive session")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Copied last agent message to clipboard") {
		t.Fatalf("stdout missing copy status: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), base64.StdEncoding.EncodeToString([]byte("last reply"))) {
		t.Fatalf("stdout missing OSC52 payload: %q", stdout.String())
	}
}

func TestShareSessionHTMLReportsGistFailure(t *testing.T) {
	restore := replaceProcessHooks(t)
	defer restore()

	session, err := NewSessionManager(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	externalLookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	externalCommand = func(_ context.Context, _ string, args []string, _ string) (commandResult, error) {
		if len(args) >= 2 && args[0] == "auth" {
			return commandResult{ExitCode: 0}, nil
		}
		return commandResult{Stderr: "boom", ExitCode: 1}, nil
	}
	_, err = ShareSessionHTML(context.Background(), session.File())
	if err == nil || !strings.Contains(err.Error(), "failed to create gist: boom") {
		t.Fatalf("share error = %v", err)
	}
}

func TestCopyCommandErrorsWithoutAssistantMessage(t *testing.T) {
	agent := &AgentSession{Session: InMemorySession(t.TempDir())}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	_, err := handleSlash(context.Background(), agent, "/copy", &stdout, &stderr)
	if err == nil || err.Error() != "No agent messages to copy yet." {
		t.Fatalf("copy error = %v", err)
	}
}

func replaceProcessHooks(t *testing.T) func() {
	t.Helper()
	oldCommand := externalCommand
	oldLookPath := externalLookPath
	oldEnv := envValue
	oldGOOS := goosValue
	return func() {
		externalCommand = oldCommand
		externalLookPath = oldLookPath
		envValue = oldEnv
		goosValue = oldGOOS
	}
}

func TestGetShareViewerURLAddsSlash(t *testing.T) {
	restore := replaceProcessHooks(t)
	defer restore()
	envValue = func(key string) string {
		if key == shareViewerURLEnvironment {
			return "https://viewer.example/session"
		}
		return ""
	}
	raw, _ := json.Marshal(GetShareViewerURL("abc"))
	if string(raw) != `"https://viewer.example/session/abc"` {
		t.Fatalf("share viewer URL JSON = %s", raw)
	}
}
