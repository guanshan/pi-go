package cli

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestParseArgs(t *testing.T) {
	args := ParseArgs([]string{
		"--provider", "openai",
		"--model", "gpt-4.1:high",
		"--thinking", "low",
		"--tools", "read,grep,find,ls",
		"--append-system-prompt", "extra",
		"@README.md",
		"hello",
		"--custom-flag=value",
	})
	if args.Provider != "openai" || args.Model != "gpt-4.1:high" {
		t.Fatalf("model args not parsed: %#v", args)
	}
	if !args.HasThinking || args.Thinking != ai.ThinkingLow {
		t.Fatalf("thinking not parsed: %#v", args)
	}
	if len(args.Tools) != 4 || args.Tools[1] != "grep" {
		t.Fatalf("tools not parsed: %#v", args.Tools)
	}
	if len(args.FileArgs) != 1 || args.FileArgs[0] != "README.md" {
		t.Fatalf("file args not parsed: %#v", args.FileArgs)
	}
	if len(args.Messages) != 1 || args.Messages[0] != "hello" {
		t.Fatalf("messages not parsed: %#v", args.Messages)
	}
	if args.UnknownFlags["custom-flag"] != "value" {
		t.Fatalf("unknown flag not retained: %#v", args.UnknownFlags)
	}
}

func TestParseArgsNameSessionIDExcludeTools(t *testing.T) {
	args := ParseArgs([]string{
		"-n", "my session",
		"--session-id", "fixed-id.1",
		"-xt", "bash, write ,",
	})
	if args.Name != "my session" {
		t.Fatalf("name=%q", args.Name)
	}
	if args.SessionID != "fixed-id.1" {
		t.Fatalf("sessionID=%q", args.SessionID)
	}
	if len(args.ExcludeTools) != 2 || args.ExcludeTools[0] != "bash" || args.ExcludeTools[1] != "write" {
		t.Fatalf("excludeTools=%#v", args.ExcludeTools)
	}
	for _, d := range args.Diagnostics {
		if d.Type == "error" {
			t.Fatalf("unexpected diagnostic: %#v", d)
		}
	}
}

func TestParseArgsNameRequiresValue(t *testing.T) {
	args := ParseArgs([]string{"hello", "--name"})
	found := false
	for _, d := range args.Diagnostics {
		if d.Type == "error" && d.Message == "--name requires a value" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected --name requires a value diagnostic, got %#v", args.Diagnostics)
	}
}
