package core

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

// printModeProbeTool is a no-op tool the scripted faux toolCall step targets so
// the agent loop runs a second turn after the tool result, exercising the
// multi-turn-with-tool-call path.
type printModeProbeTool struct{}

func (printModeProbeTool) Name() string           { return "probe" }
func (printModeProbeTool) Description() string    { return "probe" }
func (printModeProbeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (printModeProbeTool) Execute(_ context.Context, _ json.RawMessage, _ catools.ToolUpdate) ai.ToolResult {
	return ai.ToolResult{Content: ai.TextBlocks("probe ran")}
}

// printModeRuntime builds an AgentSessionRuntime backed by the faux provider and
// the given tool set, mirroring testInteractiveRuntime.
func printModeRuntime(t *testing.T, tools ToolSet) *AgentSessionRuntime {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	session := NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, tools, "system")
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

// TestRunPrintModeTextFinalAssistantOnly locks the TS print-mode (text) output
// contract for a multi-turn-with-tool-call scenario (print-mode.ts:128-145):
// stdout carries ONLY the final assistant text (no streaming, no intermediate
// turn text), and stderr carries no per-turn "[tool]" noise. The first turn
// drives a tool call (which loops into a second turn), and the post-tool turn
// produces the final text. A second prompt then produces the actual final text;
// only that last assistant message must reach stdout.
func TestRunPrintModeTextFinalAssistantOnly(t *testing.T) {
	ai.SetFauxResponses([]ai.FauxResponse{
		// Turn 1 (prompt "a"): emits a tool call -> agent runs the tool...
		{Content: []ai.ContentBlock{ai.FauxToolCall("call_1", "probe", map[string]any{})}},
		// ...then the post-tool model turn produces intermediate text.
		{Content: []ai.ContentBlock{ai.FauxText("intermediate from turn a")}},
		// Turn 2 (prompt "b"): the final assistant text.
		{Content: []ai.ContentBlock{ai.FauxText("FINAL ANSWER")}},
	})
	t.Cleanup(ai.ResetFauxResponses)

	runtime := printModeRuntime(t, ToolSet{"probe": printModeProbeTool{}})
	var stdout, stderr bytes.Buffer

	exit, err := RunPrintMode(context.Background(), runtime, cli.ModeText, "a", nil, &stdout, &stderr, []string{"b"})
	if err != nil {
		t.Fatalf("RunPrintMode err=%v", err)
	}
	if exit != 0 {
		t.Fatalf("exit=%d, want 0", exit)
	}

	gotOut := stdout.String()
	if gotOut != "FINAL ANSWER\n" {
		t.Fatalf("stdout=%q, want %q (final assistant text only)", gotOut, "FINAL ANSWER\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q, want empty (no [tool] noise)", stderr.String())
	}
}

// TestRunPrintModeTextErrorStopReason locks print-mode.ts:134-136: when the
// final assistant message stopped with an error, the errorMessage goes to
// stderr and the exit code is 1, with nothing on stdout.
func TestRunPrintModeTextErrorStopReason(t *testing.T) {
	ai.SetFauxResponses([]ai.FauxResponse{
		{StopReason: "error", ErrorMessage: "boom from provider"},
	})
	t.Cleanup(ai.ResetFauxResponses)

	runtime := printModeRuntime(t, ToolSet{})
	var stdout, stderr bytes.Buffer

	exit, err := RunPrintMode(context.Background(), runtime, cli.ModeText, "go", nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrintMode err=%v", err)
	}
	if exit != 1 {
		t.Fatalf("exit=%d, want 1", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q, want empty on error", stdout.String())
	}
	if stderr.String() != "boom from provider\n" {
		t.Fatalf("stderr=%q, want %q", stderr.String(), "boom from provider\n")
	}
}

// TestRunPrintModeTextErrorStopReasonFallbackMessage locks the fallback wording
// when the errored assistant message carries no errorMessage
// (print-mode.ts:135: `Request ${stopReason}`).
func TestRunPrintModeTextErrorStopReasonFallbackMessage(t *testing.T) {
	ai.SetFauxResponses([]ai.FauxResponse{
		{Content: []ai.ContentBlock{ai.FauxText("partial")}, StopReason: "aborted"},
	})
	t.Cleanup(ai.ResetFauxResponses)

	runtime := printModeRuntime(t, ToolSet{})
	var stdout, stderr bytes.Buffer

	exit, err := RunPrintMode(context.Background(), runtime, cli.ModeText, "go", nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunPrintMode err=%v", err)
	}
	if exit != 1 {
		t.Fatalf("exit=%d, want 1", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q, want empty on aborted", stdout.String())
	}
	if stderr.String() != "Request aborted\n" {
		t.Fatalf("stderr=%q, want %q", stderr.String(), "Request aborted\n")
	}
}
