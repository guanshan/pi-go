package core

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

func newExecuteBashAgent(t *testing.T) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	return NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}

// TestExecuteBashTruncatesLargeOutput locks 8.md P1-1: the RPC/SDK bash path must
// truncate output exceeding the default byte limit, set Truncated, and spill the
// full output to a readable temp file (mirroring bash-executor.ts).
func TestExecuteBashTruncatesLargeOutput(t *testing.T) {
	agent := newExecuteBashAgent(t)
	// Emit well over DefaultMaxBytes (50KB) of output.
	lines := (catools.DefaultMaxBytes / 10) + 500
	cmd := "for i in $(seq 1 " + strconv.Itoa(lines) + "); do echo 0123456789; done"
	res, err := agent.ExecuteBash(context.Background(), cmd, BashRunOptions{ExcludeFromContext: true})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated=true for %d lines, got false (output %d bytes)", lines, len(res.Output))
	}
	if len(res.Output) > catools.DefaultMaxBytes+1024 {
		t.Fatalf("truncated output still too large: %d bytes", len(res.Output))
	}
	if res.FullOutputPath == "" {
		t.Fatal("expected FullOutputPath to be set on truncation")
	}
	full, err := os.ReadFile(res.FullOutputPath)
	if err != nil {
		t.Fatalf("read full output file: %v", err)
	}
	if len(full) <= len(res.Output) {
		t.Fatalf("full output (%d) should exceed truncated output (%d)", len(full), len(res.Output))
	}
	if !strings.Contains(string(full), "0123456789") {
		t.Fatal("full output file missing expected content")
	}
}

// TestExecuteBashStripsAnsiAndControlChars locks 8.md P1-1's sanitization: ANSI
// escape sequences, control bytes, and carriage returns must be stripped from
// the RPC/SDK bash output (mirroring sanitizeBinaryOutput(stripAnsi(...)).replace(/\r/g,"")).
func TestExecuteBashStripsAnsiAndControlChars(t *testing.T) {
	agent := newExecuteBashAgent(t)
	// printf an ANSI color sequence, a NUL byte, and a CR.
	res, err := agent.ExecuteBash(context.Background(), `printf '\033[31mRED\033[0m\000\rTAIL'`, BashRunOptions{ExcludeFromContext: true})
	if err != nil {
		t.Fatalf("ExecuteBash: %v", err)
	}
	if strings.Contains(res.Output, "\x1b") {
		t.Fatalf("ANSI escape not stripped: %q", res.Output)
	}
	if strings.ContainsRune(res.Output, '\x00') {
		t.Fatalf("NUL byte not stripped: %q", res.Output)
	}
	if strings.ContainsRune(res.Output, '\r') {
		t.Fatalf("carriage return not stripped: %q", res.Output)
	}
	// The visible text survives sanitization.
	if !strings.Contains(res.Output, "RED") || !strings.Contains(res.Output, "TAIL") {
		t.Fatalf("visible text lost during sanitization: %q", res.Output)
	}
}
