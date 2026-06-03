package core

import (
	"context"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// cycleTestModels returns two configured models for exercising the Ctrl+P /
// Shift+Ctrl+P cycle paths. AvailableConfigured iterates the registry slice in
// order, so cycle direction is deterministic.
func cycleTestModels() []ai.Model {
	return []ai.Model{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"},
		{Provider: "openai", ID: "gpt-5", API: "openai"},
	}
}

// TestCycleModelBackwardWrapsAndCycles locks the Shift+Ctrl+P path: a backward
// step moves off the current model (Euclidean wrap so index 0 -> last), and a
// second backward step returns to the start.
func TestCycleModelBackwardWrapsAndCycles(t *testing.T) {
	models := cycleTestModels()
	agent, _ := buildPersistAgent(t, models, models[0])
	start := agent.CurrentModel()

	if _, ok := agent.CycleModelBackward(); !ok {
		t.Fatalf("CycleModelBackward returned false with 2 models")
	}
	current := agent.CurrentModel()
	if current.Provider == start.Provider && current.ID == start.ID {
		t.Fatalf("backward cycle did not change model from %s/%s", start.Provider, start.ID)
	}
	if _, ok := agent.CycleModelBackward(); !ok {
		t.Fatalf("second CycleModelBackward returned false")
	}
	current = agent.CurrentModel()
	if current.Provider != start.Provider || current.ID != start.ID {
		t.Fatalf("two backward cycles did not return to start: got %s/%s want %s/%s",
			current.Provider, current.ID, start.Provider, start.ID)
	}
}

// TestCycleModelForwardBackwardRoundTrip: forward then backward returns to the
// original model, confirming the directions are inverses.
func TestCycleModelForwardBackwardRoundTrip(t *testing.T) {
	models := cycleTestModels()
	agent, _ := buildPersistAgent(t, models, models[0])
	start := agent.CurrentModel()

	if _, ok := agent.CycleModel(); !ok {
		t.Fatalf("CycleModel (forward) returned false")
	}
	current := agent.CurrentModel()
	if current.Provider == start.Provider && current.ID == start.ID {
		t.Fatalf("forward cycle did not change model")
	}
	if _, ok := agent.CycleModelBackward(); !ok {
		t.Fatalf("CycleModelBackward returned false")
	}
	current = agent.CurrentModel()
	if current.Provider != start.Provider || current.ID != start.ID {
		t.Fatalf("forward+backward did not round-trip: got %s/%s want %s/%s",
			current.Provider, current.ID, start.Provider, start.ID)
	}
}

// TestCycleModelBackwardSingleModelNoop: with a single available model, backward
// cycling reports false and leaves the model unchanged (mirrors CycleModel).
func TestCycleModelBackwardSingleModelNoop(t *testing.T) {
	models := []ai.Model{{Provider: "anthropic", ID: "claude-sonnet-4-5", API: "anthropic"}}
	agent, _ := buildPersistAgent(t, models, models[0])
	start := agent.CurrentModel()

	if data, ok := agent.CycleModelBackward(); ok || data != nil {
		t.Fatalf("CycleModelBackward with one model should return (nil,false); got (%#v,%v)", data, ok)
	}
	current := agent.CurrentModel()
	if current.Provider != start.Provider || current.ID != start.ID {
		t.Fatalf("single-model backward cycle mutated model to %s/%s", current.Provider, current.ID)
	}
}

// TestInteractiveCycleModelRunsAsync locks the deadlock fix: Ctrl+P must run the
// model switch in a tea.Cmd goroutine, not synchronously on the Update
// goroutine. CycleModel emits ModelChangedEvent -> m.post -> program.Send, which
// blocks on Bubble Tea's unbuffered msg channel, so a synchronous call from
// Update would freeze the TUI. cycleModel must therefore return a non-nil cmd
// and set the cyclingModel guard.
func TestInteractiveCycleModelRunsAsync(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := model.cycleModel(false)
	if cmd == nil {
		t.Fatal("cycleModel must return a tea.Cmd so the switch runs off the Update goroutine (a synchronous CycleModel deadlocks on program.Send)")
	}
	if !model.cyclingModel {
		t.Fatal("cyclingModel guard should be set while the cycle cmd is in flight")
	}
	if _, ok := cmd().(modelCycleDoneMsg); !ok {
		t.Fatal("the cycle cmd should yield a modelCycleDoneMsg")
	}
}
