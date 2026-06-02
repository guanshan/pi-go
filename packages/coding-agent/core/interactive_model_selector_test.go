package core

import (
	"context"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// selectorTestModels returns three configured models spanning two providers so
// the overlay tests can exercise filtering across provider/id and navigation
// wrap-around.
func selectorTestModels() []ai.Model {
	return []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic", Name: "Claude Sonnet"},
		{Provider: "openai", ID: "gpt-5", API: "openai", Name: "GPT-5"},
		{Provider: "openai", ID: "gpt-5-mini", API: "openai", Name: "GPT-5 Mini"},
	}
}

// values collects the current (filtered) item values in display order.
func selectorValues(o *modelSelectorOverlay) []string {
	out := make([]string, 0, len(o.list.Items()))
	for _, item := range o.list.Items() {
		out = append(out, item.Value)
	}
	return out
}

func TestModelSelectorMarksCurrent(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[1]) // openai/gpt-5
	if overlay == nil {
		t.Fatal("overlay should be constructed for >0 models")
	}
	value, ok := overlay.SelectedValue()
	if !ok {
		t.Fatal("expected a selected value")
	}
	if value != "openai/gpt-5" {
		t.Fatalf("overlay did not start on the current model: got %q", value)
	}
}

func TestModelSelectorFilterNarrows(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[0])

	// Typing "gpt" should match the two openai/gpt-* entries by substring, even
	// though their Value prefix is "openai/" (SelectList.SetFilter's prefix
	// match would miss them).
	for _, r := range "gpt" {
		if action := overlay.HandleKey(string(r)); action != modelSelectorNone {
			t.Fatalf("typing %q produced action %v, want none", string(r), action)
		}
	}
	got := selectorValues(overlay)
	want := []string{"openai/gpt-5", "openai/gpt-5-mini"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("filter 'gpt' = %v, want %v", got, want)
	}

	// Backspace widens the filter back out to the full set.
	overlay.HandleKey("backspace")
	overlay.HandleKey("backspace")
	overlay.HandleKey("backspace")
	if got := len(selectorValues(overlay)); got != len(models) {
		t.Fatalf("after clearing filter, %d items remain, want %d", got, len(models))
	}
}

func TestModelSelectorFilterNoMatchThenName(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[0])

	// "Mini" only appears in the Name/Label, confirming the filter searches the
	// label as well as the value.
	for _, r := range "mini" {
		overlay.HandleKey(string(r))
	}
	got := selectorValues(overlay)
	if len(got) != 1 || got[0] != "openai/gpt-5-mini" {
		t.Fatalf("filter 'mini' = %v, want [openai/gpt-5-mini]", got)
	}
}

func TestModelSelectorNavigationWraps(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[0]) // starts at index 0

	first, _ := overlay.SelectedValue()
	if first != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("unexpected starting selection %q", first)
	}

	// Up from the first item wraps to the last.
	overlay.HandleKey("up")
	last, _ := overlay.SelectedValue()
	if last != "openai/gpt-5-mini" {
		t.Fatalf("up from first did not wrap to last: got %q", last)
	}

	// Down from the last item wraps back to the first.
	overlay.HandleKey("down")
	wrapped, _ := overlay.SelectedValue()
	if wrapped != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("down from last did not wrap to first: got %q", wrapped)
	}

	// One down moves to the second entry.
	overlay.HandleKey("down")
	second, _ := overlay.SelectedValue()
	if second != "openai/gpt-5" {
		t.Fatalf("down did not advance to second entry: got %q", second)
	}
}

func TestModelSelectorEnterReportsSelection(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[0])
	overlay.HandleKey("down") // move to openai/gpt-5

	if action := overlay.HandleKey("enter"); action != modelSelectorSelect {
		t.Fatalf("enter produced action %v, want select", action)
	}
	value, ok := overlay.SelectedValue()
	if !ok || value != "openai/gpt-5" {
		t.Fatalf("selected value = %q (ok=%v), want openai/gpt-5", value, ok)
	}
}

func TestModelSelectorEscCancels(t *testing.T) {
	models := selectorTestModels()
	overlay := newModelSelectorOverlay(models, models[0])
	if action := overlay.HandleKey("esc"); action != modelSelectorCancel {
		t.Fatalf("esc produced action %v, want cancel", action)
	}
	if action := overlay.HandleKey("ctrl+c"); action != modelSelectorCancel {
		t.Fatalf("ctrl+c produced action %v, want cancel", action)
	}
}

func TestModelSelectorEmptyForNoModels(t *testing.T) {
	if overlay := newModelSelectorOverlay(nil, ai.Model{}); overlay != nil {
		t.Fatal("overlay should be nil when there are no models")
	}
}

// selectorInteractiveRuntime builds an interactive runtime whose agent is backed
// by a real multi-model registry with configured auth, so SetModel can resolve
// the chosen provider/id. Mirrors buildPersistAgent's registry wiring.
func selectorInteractiveRuntime(t *testing.T, models []ai.Model, current ai.Model) *AgentSessionRuntime {
	t.Helper()
	agent, settings := buildPersistAgent(t, models, current)
	return &AgentSessionRuntime{
		session: agent,
		services: &AgentSessionServices{
			Cwd:             settings.CWD,
			AgentDir:        settings.AgentDir,
			SettingsManager: settings,
			ModelRegistry:   agent.Registry,
			ResourceLoader:  agent.Resources,
		},
	}
}

// TestInteractiveModelSelectorSelectRunsAsync is the slice-2 analogue of
// TestInteractiveCycleModelRunsAsync: selecting a model from the overlay must
// run SetModel in a tea.Cmd goroutine, never synchronously on the Update
// goroutine. SetModel emits ModelChangedEvent -> m.post -> program.Send, which
// blocks on Bubble Tea's unbuffered msg channel whose only reader is the Update
// goroutine, so an inline call would deadlock. Enter must therefore close the
// overlay and hand back a non-nil cmd that yields a modelSelectDoneMsg.
func TestInteractiveModelSelectorSelectRunsAsync(t *testing.T) {
	models := selectorTestModels()
	runtime := selectorInteractiveRuntime(t, models, models[0])
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	model.openModelSelector()
	if model.modelSelector == nil {
		t.Fatal("openModelSelector did not open the overlay with multiple models available")
	}

	// Highlight a different model, then confirm.
	model.modelSelector.HandleKey("down")
	target, ok := model.modelSelector.SelectedValue()
	if !ok || target == models[0].Provider+"/"+models[0].ID {
		t.Fatalf("expected to highlight a non-current model, got %q", target)
	}

	cmd := model.handleModelSelectorKey("enter")
	if cmd == nil {
		t.Fatal("selecting a model must return a tea.Cmd so SetModel runs off the Update goroutine (a synchronous SetModel deadlocks on program.Send)")
	}
	if model.modelSelector != nil {
		t.Fatal("overlay should close once a selection is confirmed")
	}
	msg := cmd()
	if _, ok := msg.(modelSelectDoneMsg); !ok {
		t.Fatalf("selection cmd yielded %T, want modelSelectDoneMsg", msg)
	}
	if done := msg.(modelSelectDoneMsg); done.Err != nil {
		t.Fatalf("selection cmd reported error: %v", done.Err)
	}
	// The async cmd actually switched the model.
	if agent := runtime.Session(); agent.Model.Provider+"/"+agent.Model.ID != target {
		t.Fatalf("model not switched: got %s/%s, want %s", agent.Model.Provider, agent.Model.ID, target)
	}
}

// TestInteractiveModelSelectorEscClearsOverlay confirms the Update-loop key
// route closes the overlay on cancel without returning a cmd.
func TestInteractiveModelSelectorEscClearsOverlay(t *testing.T) {
	models := selectorTestModels()
	runtime := selectorInteractiveRuntime(t, models, models[0])
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.openModelSelector()
	if model.modelSelector == nil {
		t.Fatal("overlay did not open")
	}
	if cmd := model.handleModelSelectorKey("esc"); cmd != nil {
		t.Fatalf("esc returned cmd %#v, want nil", cmd)
	}
	if model.modelSelector != nil {
		t.Fatal("esc did not close the overlay")
	}
}

// TestInteractiveBareModelOpensSelector locks the bare-/model entry point: a
// `/model` submission with no argument opens the overlay instead of dispatching
// a slash command, while leaving the input cleared.
func TestInteractiveBareModelOpensSelector(t *testing.T) {
	models := selectorTestModels()
	runtime := selectorInteractiveRuntime(t, models, models[0])
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("/model")
	if cmd := model.submitInputWithBehavior(""); cmd != nil {
		t.Fatalf("bare /model returned cmd %#v, want nil (opens overlay)", cmd)
	}
	if model.modelSelector == nil {
		t.Fatal("bare /model did not open the selector overlay")
	}
	if got := strings.TrimSpace(model.input.Value()); got != "" {
		t.Fatalf("input not cleared after opening selector: %q", got)
	}
	if model.busy {
		t.Fatal("opening the selector should not mark the model busy")
	}
}

// TestInteractiveModelWithArgStillDispatches confirms `/model provider/id`
// (with an argument) bypasses the overlay and routes to the slash handler.
func TestInteractiveModelWithArgStillDispatches(t *testing.T) {
	models := selectorTestModels()
	runtime := selectorInteractiveRuntime(t, models, models[0])
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.input.SetValue("/model openai/gpt-5")
	cmd := model.submitInputWithBehavior("")
	if cmd == nil {
		t.Fatal("/model with an argument should dispatch a slash command cmd")
	}
	if model.modelSelector != nil {
		t.Fatal("/model with an argument should not open the overlay")
	}
	if model.busyKind != interactiveBusyCommand {
		t.Fatalf("busyKind = %q, want command", model.busyKind)
	}
}
