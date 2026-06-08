package extensions

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestEventBusOrderAndUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	var order []string
	off1 := bus.On("evt", func(any) { order = append(order, "a") })
	bus.On("evt", func(any) { order = append(order, "b") })
	off3 := bus.On("evt", func(any) { order = append(order, "c") })

	bus.Emit("evt", nil)
	if strings.Join(order, "") != "abc" {
		t.Fatalf("registration order not preserved: %v", order)
	}

	// Unsubscribe the first and last; the middle one remains and no nil hole is
	// left behind.
	off1()
	off3()
	order = nil
	bus.Emit("evt", nil)
	if strings.Join(order, "") != "b" {
		t.Fatalf("after unsubscribe expected only b, got %v", order)
	}
}

func TestEventBusUnsubscribeShrinksStorage(t *testing.T) {
	bus := NewEventBus()
	offs := make([]func(), 0, 100)
	for i := 0; i < 100; i++ {
		offs = append(offs, bus.On("evt", func(any) {}))
	}
	for _, off := range offs {
		off()
	}
	bus.mu.RLock()
	_, present := bus.listeners["evt"]
	bus.mu.RUnlock()
	if present {
		t.Fatal("expected the event key to be removed once all listeners unsubscribed (no accumulating holes)")
	}
}

func TestEventBusConcurrentRegisterUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			off := bus.On("evt", func(any) {})
			bus.Emit("evt", nil)
			off()
		}()
	}
	wg.Wait()
	bus.mu.RLock()
	remaining := len(bus.listeners["evt"])
	bus.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("expected all listeners removed, got %d", remaining)
	}
}

func TestAutocompleteApplyUsesStableProviderIDAfterProviderListChanges(t *testing.T) {
	api := NewAPI()
	runner := NewRunnerWithAPI(api)
	ctx := context.Background()

	api.RegisterAutocompleteProvider(AutocompleteProviderDefinition{
		Source: "first",
		Suggest: func(context.Context, AutocompleteRequest) (AutocompleteSuggestions, error) {
			return AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "first"}}}, nil
		},
		Apply: func(context.Context, AutocompleteApplyRequest) (AutocompleteApplyResult, error) {
			return AutocompleteApplyResult{Input: "applied-first"}, nil
		},
	})
	suggestions, err := runner.Autocomplete(ctx, AutocompleteRequest{Input: "f"})
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions.Items) != 1 {
		t.Fatalf("suggestions=%#v", suggestions)
	}
	if suggestions.Items[0].ProviderID == 0 {
		t.Fatalf("suggestion missing stable provider id: %#v", suggestions.Items[0])
	}

	api.mu.Lock()
	api.Autocomplete = append([]AutocompleteProviderDefinition{{
		Source: "first",
		Suggest: func(context.Context, AutocompleteRequest) (AutocompleteSuggestions, error) {
			return AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "inserted"}}}, nil
		},
		Apply: func(context.Context, AutocompleteApplyRequest) (AutocompleteApplyResult, error) {
			return AutocompleteApplyResult{Input: "wrong-provider"}, nil
		},
	}}, api.Autocomplete...)
	api.mu.Unlock()

	applied, ok, err := runner.ApplyAutocomplete(ctx, AutocompleteApplyRequest{Item: suggestions.Items[0]})
	if err != nil || !ok {
		t.Fatalf("apply ok=%v err=%v", ok, err)
	}
	if applied.Input != "applied-first" {
		t.Fatalf("applied input=%q, want original provider", applied.Input)
	}
}
