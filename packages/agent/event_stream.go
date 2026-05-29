package agent

import "sync"

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
type EventStream[T any, R any] struct {
	events chan T
	done   chan struct{}
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []T
	once   sync.Once
	result R
	ended  bool
}

func NewEventStream[T any, R any](buffer int) *EventStream[T, R] {
	if buffer < 1 {
		buffer = 1
	}
	stream := &EventStream[T, R]{
		events: make(chan T, buffer),
		done:   make(chan struct{}),
	}
	stream.cond = sync.NewCond(&stream.mu)
	return stream
}

// Events returns the channel of streamed events. The first call starts the
// dispatch goroutine; the channel is closed after End is called and every queued
// event has been delivered.
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

// Result blocks until End is called and returns the terminal result.
func (s *EventStream[T, R]) Result() R {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func (s *EventStream[T, R]) dispatch() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.ended {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && s.ended {
			s.mu.Unlock()
			close(s.events)
			return
		}
		event := s.queue[0]
		copy(s.queue, s.queue[1:])
		var zero T
		s.queue[len(s.queue)-1] = zero
		s.queue = s.queue[:len(s.queue)-1]
		s.mu.Unlock()
		s.events <- event
	}
}
