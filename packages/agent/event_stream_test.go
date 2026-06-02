package agent

import (
	"context"
	"testing"

	"go.uber.org/goleak"
)

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

// P1-C2 (topic 6): the dispatch goroutine must not leak when a consumer
// abandons the stream before it has been drained. A consumer that reads a
// single event then stops would block dispatch forever on the bare channel
// send. Result() cancels the stream's context, which must release dispatch.
func TestEventStreamDispatchExitsWhenConsumerAbandons(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"))

	// Buffer of 1 so the channel fills after the first event; the dispatch
	// goroutine then blocks on the second send until the context is cancelled.
	stream := NewEventStream[int, string](1)
	for i := 0; i < 8; i++ {
		stream.Push(i)
	}
	stream.End("done")

	// Consume exactly one event, then abandon the stream without draining it.
	events := stream.Events()
	if got := <-events; got != 0 {
		t.Fatalf("first event=%d", got)
	}

	// Result() cancels the context; dispatch must observe it and return even
	// though queued events were never read. goleak (deferred) asserts no leak.
	if got := stream.Result(); got != "done" {
		t.Fatalf("result=%q", got)
	}
}

// The dispatch goroutine must also exit when the owning context is cancelled
// directly, independent of End/Result, so an aborted loop reclaims it.
func TestEventStreamDispatchExitsOnContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"))

	ctx, cancel := context.WithCancel(context.Background())
	stream := NewEventStreamWithContext[int, string](ctx, 1)
	for i := 0; i < 8; i++ {
		stream.Push(i)
	}
	// Stream never ends; consumer reads one event then abandons it.
	events := stream.Events()
	if got := <-events; got != 0 {
		t.Fatalf("first event=%d", got)
	}

	// Cancelling the context must release the blocked dispatch goroutine and
	// close the events channel even though End was never called.
	cancel()
	for range events {
		// Drain whatever is delivered before the channel closes; the loop must
		// terminate once dispatch returns and closes the channel.
	}
}
