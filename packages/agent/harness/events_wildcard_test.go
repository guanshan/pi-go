package harness

import (
	"context"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// P2-03: model/thinking/resources updates must reach wildcard SubscribeHarness
// listeners (in addition to their typed handlers), mirroring TS emitOwn which
// delivers every "own" event to subscribe() listeners.
func TestWildcardSubscriberReceivesOwnEvents(t *testing.T) {
	ctx := context.Background()
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	var sawModel, sawThinking, sawResources bool
	var typedModel bool
	h.OnModelSelect(func(ctx context.Context, ev ModelSelectEvent) error {
		typedModel = true
		return nil
	})
	h.SubscribeHarness(func(ctx context.Context, ev HarnessEvent) error {
		switch ev.(type) {
		case ModelSelectEvent:
			sawModel = true
		case ThinkingLevelSelectEvent:
			sawThinking = true
		case ResourcesUpdateEvent:
			sawResources = true
		}
		return nil
	})

	if err := h.SetModel(ctx, ai.Model{Provider: "test", ID: "m", API: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := h.SetThinkingLevel(ctx, ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	if err := h.SetResources(ctx, Resources{Skills: []Skill{{Name: "s"}}}); err != nil {
		t.Fatal(err)
	}

	if !typedModel {
		t.Fatal("typed model handler did not fire")
	}
	if !sawModel || !sawThinking || !sawResources {
		t.Fatalf("wildcard missed events: model=%v thinking=%v resources=%v", sawModel, sawThinking, sawResources)
	}
}
