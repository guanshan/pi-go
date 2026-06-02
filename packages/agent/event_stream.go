package agent

import (
	"context"
	"sync"
)

const DefaultAgentLoopEventBuffer = 32

// EventStream is a fan-in/fan-out stream of events terminated by a single
// result value. It uses the same internal queue + sync.Cond + dispatch goroutine
// model as ai.AssistantMessageEventStream so that Push and End are safe to call
// from different goroutines.
//
// The previous implementation sent directly on the events channel from Push and
// closed that same channel from End; a Push racing with End could send on a
// closed channel and panic. Here Push only appends to an internal queue under a
// mutex and End only flips a flag and closes done — neither touches the events
// channel — so concurrent Push/End calls can no longer panic. A single dispatch
// goroutine (started lazily on the first Events call) drains the queue onto the
// events channel and closes it once the stream has ended and the queue is empty.
//
// EventStream also carries a context so the dispatch goroutine cannot leak when
// a consumer abandons the stream (an error, abort, or early return) before it
// has been fully drained. The send on the events channel honours ctx.Done(),
// matching the leak guard in ai.AssistantMessageEventStream.
type EventStream[T any, R any] struct {
	ctx    context.Context
	cancel context.CancelFunc
	events chan T
	done   chan struct{}
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []T
	once   sync.Once
	result R
	ended  bool
}

// NewEventStream creates an EventStream backed by context.Background. Prefer
// NewEventStreamWithContext so the dispatch goroutine is reclaimed when the
// owning context is cancelled.
func NewEventStream[T any, R any](buffer int) *EventStream[T, R] {
	return NewEventStreamWithContext[T, R](context.Background(), buffer)
}

// NewEventStreamWithContext creates an EventStream whose dispatch goroutine
// stops once ctx is cancelled, even if the consumer has abandoned the events
// channel. Result also cancels ctx once the terminal result is available so an
// abandoning consumer cannot leak the dispatch goroutine.
func NewEventStreamWithContext[T any, R any](ctx context.Context, buffer int) *EventStream[T, R] {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	if buffer < 1 {
		buffer = 1
	}
	stream := &EventStream[T, R]{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan T, buffer),
		done:   make(chan struct{}),
	}
	stream.cond = sync.NewCond(&stream.mu)
	go stream.watchContext()
	return stream
}

// Events returns the channel of streamed events. The first call starts the
// dispatch goroutine; the channel is closed after End is called and every queued
// event has been delivered (or once the stream's context is cancelled).
func (s *EventStream[T, R]) Events() <-chan T {
	s.once.Do(func() {
		go s.dispatch()
	})
	return s.events
}

// Push enqueues an event. It is a no-op once the stream has ended. Safe to call
// concurrently with End.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, event)
	s.cond.Signal()
	s.mu.Unlock()
}

// End records the terminal result and marks the stream ended. Subsequent Push
// calls are dropped. Safe to call concurrently with Push; the dispatch goroutine
// still drains any already-queued events before closing the channel.
func (s *EventStream[T, R]) End(result R) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.result = result
	s.ended = true
	close(s.done)
	s.cond.Signal()
	s.mu.Unlock()
}

// Result blocks until End is called and returns the terminal result. It also
// cancels the stream's context so the dispatch goroutine is reclaimed even if
// the consumer stopped reading the events channel before it was drained.
func (s *EventStream[T, R]) Result() R {
	<-s.done
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// watchContext wakes the dispatch goroutine out of cond.Wait once the stream's
// context is cancelled so it can observe the cancellation and exit.
func (s *EventStream[T, R]) watchContext() {
	select {
	case <-s.ctx.Done():
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	case <-s.done:
	}
}

func (s *EventStream[T, R]) dispatch() {
	defer close(s.events)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.ended && s.ctx.Err() == nil {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && (s.ended || s.ctx.Err() != nil) {
			s.mu.Unlock()
			return
		}
		event := s.queue[0]
		copy(s.queue, s.queue[1:])
		var zero T
		s.queue[len(s.queue)-1] = zero
		s.queue = s.queue[:len(s.queue)-1]
		s.mu.Unlock()

		// Prefer delivering an already-dequeued event over honouring
		// cancellation: Result cancels the context once the stream ends, and a
		// racing select could otherwise drop terminal/queued events that a
		// draining consumer is still entitled to. The ctx.Done() branch only
		// guards against a leaked goroutine when the consumer abandons the
		// stream (the send would otherwise block indefinitely).
		select {
		case s.events <- event:
			continue
		default:
		}
		select {
		case s.events <- event:
		case <-s.ctx.Done():
			return
		}
	}
}
