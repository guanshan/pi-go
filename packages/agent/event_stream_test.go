package agent

import "testing"

func TestEventStreamResultAfterEventsClose(t *testing.T) {
	stream := NewEventStream[string, int](2)
	stream.Push("one")
	stream.Push("two")
	stream.End(42)

	var events []string
	for event := range stream.Events() {
		events = append(events, event)
	}
	if len(events) != 2 || events[0] != "one" || events[1] != "two" {
		t.Fatalf("events=%#v", events)
	}
	if result := stream.Result(); result != 42 {
		t.Fatalf("result=%d", result)
	}
}
