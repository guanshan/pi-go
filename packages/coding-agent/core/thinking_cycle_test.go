package core

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestCycleThinkingBusyGuard verifies Shift+Tab's cycleThinking is suppressed
// while a response is streaming, with the matching status, and never deadlocks
// (it returns no tea.Cmd in the busy case rather than calling the agent inline).
func TestCycleThinkingBusyGuard(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.busy = true
	if cmd := model.cycleThinking(); cmd != nil {
		t.Fatal("cycleThinking must not return a cmd while busy")
	}
	if !strings.Contains(model.statusMessage, "while a response is streaming") {
		t.Fatalf("expected busy status, got %q", model.statusMessage)
	}
}

// TestCycleThinkingRunsOffGoroutine verifies the non-busy path returns a tea.Cmd
// (which must run off the Update goroutine to avoid the program.Send deadlock)
// that yields a thinkingCycleDoneMsg. On a model with a single thinking level,
// the done-msg reports ok=false so the "no other levels" status is surfaced.
func TestCycleThinkingRunsOffGoroutine(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := model.cycleThinking()
	if cmd == nil {
		t.Fatal("cycleThinking should return a cmd when idle")
	}
	if !model.cyclingThinking {
		t.Fatal("cyclingThinking guard should be set before the goroutine runs")
	}
	msg := cmd()
	done, ok := msg.(thinkingCycleDoneMsg)
	if !ok {
		t.Fatalf("expected thinkingCycleDoneMsg, got %T", msg)
	}
	// Feed the done-msg back through Update to clear the guard and surface status.
	updated, _ := model.Update(done)
	m := updated.(*interactiveModel)
	if m.cyclingThinking {
		t.Fatal("cyclingThinking guard should clear on the done-msg")
	}
	if !done.ok && !strings.Contains(m.statusMessage, "No other thinking levels") {
		t.Fatalf("expected no-other-levels status, got %q", m.statusMessage)
	}
}

// TestSlashThinkingSetsAndReportsLevel exercises the /thinking slash command on
// a thinking-capable model: an explicit valid level is applied and reported, and
// a bogus level errors without mutating state.
func TestSlashThinkingSetsAndReportsLevel(t *testing.T) {
	agent := newThinkingSlashTestAgent(t)

	var stdout, stderr bytes.Buffer
	if _, err := handleSlash(context.Background(), agent, "/thinking high", &stdout, &stderr); err != nil {
		t.Fatalf("/thinking high: %v", err)
	}
	if agent.CurrentThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("thinking level not set to high, got %q", agent.CurrentThinkingLevel())
	}
	if !strings.Contains(stdout.String(), "Thinking: high") {
		t.Fatalf("expected level report, got %q", stdout.String())
	}

	if _, err := handleSlash(context.Background(), agent, "/thinking bogus", &stdout, &stderr); err == nil {
		t.Fatal("/thinking bogus should error")
	}
	if agent.CurrentThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("invalid /thinking must not change level, got %q", agent.CurrentThinkingLevel())
	}
}

// TestSlashThinkingCyclesWithoutArg verifies a bare /thinking cycles to the next
// level on a multi-level model.
func TestSlashThinkingCyclesWithoutArg(t *testing.T) {
	agent := newThinkingSlashTestAgent(t)
	start := agent.CurrentThinkingLevel()
	var stdout, stderr bytes.Buffer
	if _, err := handleSlash(context.Background(), agent, "/thinking", &stdout, &stderr); err != nil {
		t.Fatalf("/thinking: %v", err)
	}
	if agent.CurrentThinkingLevel() == start {
		t.Fatalf("bare /thinking should cycle the level away from %q", start)
	}
	if !strings.Contains(stdout.String(), "Thinking:") {
		t.Fatalf("expected level report, got %q", stdout.String())
	}
}

func newThinkingSlashTestAgent(t *testing.T) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	auth := ai.NewAuthStorage(settings.AgentDir)
	registry := ai.NewModelRegistry(settings.AgentDir, auth)
	model := ai.Model{
		Provider:       "faux",
		ID:             "faux",
		API:            "faux",
		Reasoning:      true,
		ThinkingLevels: []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingHigh},
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	return NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}
