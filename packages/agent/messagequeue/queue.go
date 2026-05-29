package messagequeue

import (
	"sync"

	"github.com/guanshan/pi-go/packages/ai"
)

type Mode string

const (
	ModeAll       Mode = "all"
	ModeOneAtTime Mode = "one-at-a-time"
)

type Queue struct {
	mu   sync.Mutex
	mode Mode
	msgs []ai.Message
}

func New(mode Mode) *Queue {
	if mode == "" {
		mode = ModeOneAtTime
	}
	return &Queue{mode: mode}
}

func (q *Queue) Enqueue(msg ai.Message) {
	if q == nil || msg == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, msg)
}

func (q *Queue) Drain() []ai.Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.msgs) == 0 {
		return nil
	}
	if q.mode == ModeAll {
		out := append([]ai.Message(nil), q.msgs...)
		q.msgs = nil
		return out
	}
	out := []ai.Message{q.msgs[0]}
	q.msgs = q.msgs[1:]
	return out
}

func (q *Queue) DrainMode(mode Mode) []ai.Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.msgs) == 0 {
		return nil
	}
	if mode == ModeAll {
		out := append([]ai.Message(nil), q.msgs...)
		q.msgs = nil
		return out
	}
	out := []ai.Message{q.msgs[0]}
	q.msgs = q.msgs[1:]
	return out
}

func (q *Queue) DrainAll() []ai.Message {
	return q.DrainMode(ModeAll)
}

func (q *Queue) Prepend(messages []ai.Message) {
	if q == nil || len(messages) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	next := append([]ai.Message(nil), messages...)
	q.msgs = append(next, q.msgs...)
}

func (q *Queue) Clear() []ai.Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := append([]ai.Message(nil), q.msgs...)
	q.msgs = nil
	return out
}

func (q *Queue) Snapshot() []ai.Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]ai.Message(nil), q.msgs...)
}

func (q *Queue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.msgs)
}

func (q *Queue) HasItems() bool {
	return q.Len() > 0
}

func (q *Queue) SetMode(mode Mode) {
	if mode == "" {
		mode = ModeOneAtTime
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.mode = mode
}

func (q *Queue) Mode() Mode {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.mode
}
