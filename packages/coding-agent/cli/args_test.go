package cli

import (
	"strings"
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

// TestParseArgsKnownFlagMissingValueIsPermissive mirrors TS args.ts: a known
// value-flag at the end of argv (no value) is NOT an error; it falls through to
// the unknown-flag handler and is recorded as a boolean true in UnknownFlags.
// Only --name errors. No "Missing value for X" diagnostic is emitted.
func TestParseArgsKnownFlagMissingValueIsPermissive(t *testing.T) {
	args := ParseArgs([]string{"--provider"})
	for _, d := range args.Diagnostics {
		if d.Type == "error" {
			t.Fatalf("known flag missing value should not error, got %q", d.Message)
		}
	}
	if v, ok := args.UnknownFlags["provider"]; !ok || v != true {
		t.Fatalf("trailing --provider should be a boolean unknown flag, got %#v", args.UnknownFlags)
	}
	if args.Provider != "" {
		t.Fatalf("provider should be unset when no value follows, got %q", args.Provider)
	}
}

func TestParseArgsShortValueAliasMissingValueErrors(t *testing.T) {
	for _, flag := range []string{"-t", "-e", "-xt"} {
		args := ParseArgs([]string{flag})
		found := false
		for _, d := range args.Diagnostics {
			if d.Type == "error" && d.Message == "Unknown option: "+flag {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing value should report Unknown option, diagnostics=%#v", flag, args.Diagnostics)
		}
		if len(args.UnknownFlags) != 0 {
			t.Fatalf("%s missing value should not be stored in UnknownFlags: %#v", flag, args.UnknownFlags)
		}
	}
}

// TestParseArgsUnknownFlagCapturesFollowingValue mirrors TS: an unknown --flag
// followed by a non-flag value captures that value; otherwise it is a boolean.
func TestParseArgsUnknownFlagCapturesFollowingValue(t *testing.T) {
	args := ParseArgs([]string{"--mystery", "captured", "--solo"})
	if v, ok := args.UnknownFlags["mystery"]; !ok || v != "captured" {
		t.Fatalf("unknown flag should capture following value, got %#v", args.UnknownFlags)
	}
	if v, ok := args.UnknownFlags["solo"]; !ok || v != true {
		t.Fatalf("trailing unknown flag should be boolean true, got %#v", args.UnknownFlags)
	}
}

// TestPrintHelpIncludesExamplesAndEnvDescriptions verifies the help output is on
// par with TS args.ts: it includes the Examples block, descriptioned environment
// variables, and the Built-in Tool Names section.
func TestPrintHelpIncludesExamplesAndEnvDescriptions(t *testing.T) {
	var out strings.Builder
	PrintHelp(&out, nil)
	got := out.String()
	for _, want := range []string{
		"Examples:",
		"# Interactive mode with initial prompt",
		`pi --model openai/gpt-4o "Help me refactor this code"`,
		`pi --export session.jsonl output.html`,
		"Environment Variables:",
		"ANTHROPIC_API_KEY                - Anthropic Claude API key",
		"AWS_REGION                       - AWS region for Amazon Bedrock",
		"Built-in Tool Names:",
		"read   - Read file contents",
		"ls     - List directory contents (read-only, off by default)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q", want)
		}
	}
}
