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

// TestParseArgsInvalidModeSilentlyIgnored mirrors TS args.ts:77-81: an
// unrecognized --mode value is silently ignored (Mode stays default text) and
// produces NO error diagnostic, unlike Go's earlier fatal-exit behavior.
func TestParseArgsInvalidModeSilentlyIgnored(t *testing.T) {
	args := ParseArgs([]string{"--mode", "bogus", "hi"})
	if args.Mode != ModeText {
		t.Fatalf("invalid --mode should leave Mode at default text, got %q", args.Mode)
	}
	for _, d := range args.Diagnostics {
		if d.Type == "error" {
			t.Fatalf("invalid --mode must not produce an error diagnostic, got %q", d.Message)
		}
	}
	if len(args.Messages) != 1 || args.Messages[0] != "hi" {
		t.Fatalf("trailing message should still parse, got %#v", args.Messages)
	}
	for _, mode := range []string{"text", "json", "rpc"} {
		got := ParseArgs([]string{"--mode", mode})
		if string(got.Mode) != mode {
			t.Fatalf("--mode %s should set Mode, got %q", mode, got.Mode)
		}
	}
}

// TestParseArgsNoStarBooleanFlags asserts each --no-* flag (and its short alias
// where one exists) flips the matching boolean, matching TS args.ts.
func TestParseArgsNoStarBooleanFlags(t *testing.T) {
	cases := []struct {
		argv  []string
		check func(Args) bool
		name  string
	}{
		{[]string{"--no-session"}, func(a Args) bool { return a.NoSession }, "no-session"},
		{[]string{"--no-tools"}, func(a Args) bool { return a.NoTools }, "no-tools"},
		{[]string{"-nt"}, func(a Args) bool { return a.NoTools }, "-nt"},
		{[]string{"--no-builtin-tools"}, func(a Args) bool { return a.NoBuiltinTools }, "no-builtin-tools"},
		{[]string{"-nbt"}, func(a Args) bool { return a.NoBuiltinTools }, "-nbt"},
		{[]string{"--no-extensions"}, func(a Args) bool { return a.NoExtensions }, "no-extensions"},
		{[]string{"-ne"}, func(a Args) bool { return a.NoExtensions }, "-ne"},
		{[]string{"--no-skills"}, func(a Args) bool { return a.NoSkills }, "no-skills"},
		{[]string{"-ns"}, func(a Args) bool { return a.NoSkills }, "-ns"},
		{[]string{"--no-prompt-templates"}, func(a Args) bool { return a.NoPromptTemplates }, "no-prompt-templates"},
		{[]string{"-np"}, func(a Args) bool { return a.NoPromptTemplates }, "-np"},
		{[]string{"--no-themes"}, func(a Args) bool { return a.NoThemes }, "no-themes"},
		{[]string{"--no-context-files"}, func(a Args) bool { return a.NoContextFiles }, "no-context-files"},
		{[]string{"-nc"}, func(a Args) bool { return a.NoContextFiles }, "-nc"},
	}
	for _, tc := range cases {
		args := ParseArgs(tc.argv)
		if !tc.check(args) {
			t.Errorf("%s did not flip its boolean: %#v", tc.name, args)
		}
		for _, d := range args.Diagnostics {
			if d.Type == "error" {
				t.Errorf("%s produced unexpected error diagnostic: %q", tc.name, d.Message)
			}
		}
	}
}

// TestParseArgsValueFlagsAndAliases covers value-bearing flags, their short
// aliases, repeatable flags, --print message capture, and --list-models'
// optional argument.
func TestParseArgsValueFlagsAndAliases(t *testing.T) {
	t.Run("repeatable flags accumulate", func(t *testing.T) {
		args := ParseArgs([]string{
			"--extension", "a", "-e", "b",
			"--skill", "s1", "--skill", "s2",
			"--prompt-template", "p1",
			"--theme", "t1",
			"--append-system-prompt", "x", "--append-system-prompt", "y",
		})
		if len(args.Extensions) != 2 || args.Extensions[0] != "a" || args.Extensions[1] != "b" {
			t.Fatalf("extensions=%#v", args.Extensions)
		}
		if len(args.Skills) != 2 || args.Skills[1] != "s2" {
			t.Fatalf("skills=%#v", args.Skills)
		}
		if len(args.PromptTemplates) != 1 || len(args.Themes) != 1 {
			t.Fatalf("promptTemplates=%#v themes=%#v", args.PromptTemplates, args.Themes)
		}
		if len(args.AppendSystemPrompt) != 2 || args.AppendSystemPrompt[1] != "y" {
			t.Fatalf("appendSystemPrompt=%#v", args.AppendSystemPrompt)
		}
	})
	t.Run("print captures following message", func(t *testing.T) {
		args := ParseArgs([]string{"-p", "do the thing"})
		if !args.Print || len(args.Messages) != 1 || args.Messages[0] != "do the thing" {
			t.Fatalf("print=%v messages=%#v", args.Print, args.Messages)
		}
	})
	t.Run("print without message stays empty", func(t *testing.T) {
		args := ParseArgs([]string{"--print"})
		if !args.Print || len(args.Messages) != 0 {
			t.Fatalf("print=%v messages=%#v", args.Print, args.Messages)
		}
	})
	t.Run("list-models without search", func(t *testing.T) {
		args := ParseArgs([]string{"--list-models"})
		if args.ListModels == nil || *args.ListModels != "" {
			t.Fatalf("list-models=%v", args.ListModels)
		}
	})
	t.Run("list-models with search", func(t *testing.T) {
		args := ParseArgs([]string{"--list-models", "sonnet"})
		if args.ListModels == nil || *args.ListModels != "sonnet" {
			t.Fatalf("list-models=%v", args.ListModels)
		}
	})
	t.Run("continue and resume aliases", func(t *testing.T) {
		if !ParseArgs([]string{"-c"}).Continue || !ParseArgs([]string{"--continue"}).Continue {
			t.Fatal("continue alias failed")
		}
		if !ParseArgs([]string{"-r"}).Resume || !ParseArgs([]string{"--resume"}).Resume {
			t.Fatal("resume alias failed")
		}
	})
	t.Run("fork and session-dir", func(t *testing.T) {
		args := ParseArgs([]string{"--fork", "abc", "--session-dir", "/tmp/sd"})
		if args.Fork != "abc" || args.SessionDir != "/tmp/sd" {
			t.Fatalf("fork=%q sessionDir=%q", args.Fork, args.SessionDir)
		}
	})
	t.Run("export with output message", func(t *testing.T) {
		args := ParseArgs([]string{"--export", "session.jsonl", "out.html"})
		if args.Export != "session.jsonl" {
			t.Fatalf("export=%q", args.Export)
		}
		if len(args.Messages) != 1 || args.Messages[0] != "out.html" {
			t.Fatalf("export output message=%#v", args.Messages)
		}
	})
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
