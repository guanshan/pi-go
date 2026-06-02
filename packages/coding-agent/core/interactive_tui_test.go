package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/guanshan/pi-go/packages/ai"
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
