package harness

import (
	"context"
	"sort"

	"github.com/guanshan/pi-go/packages/agent"
)

type HarnessEvent interface {
	harnessEventTag()
}

type QueueUpdateEvent struct {
	Steer    []agent.AgentMessage
	FollowUp []agent.AgentMessage
	NextTurn []agent.AgentMessage
}

type SavePointEvent struct {
	HadPendingMutations bool
}

type AbortEvent struct {
	ClearedSteer    []agent.AgentMessage
	ClearedFollowUp []agent.AgentMessage
}

type SettledEvent struct {
	NextTurnCount int
}

type ToolsUpdateEvent struct {
	ToolNames               []string
	PreviousToolNames       []string
	ActiveToolNames         []string
	PreviousActiveToolNames []string
	Source                  string
}

func (QueueUpdateEvent) harnessEventTag() {}
func (SavePointEvent) harnessEventTag()   {}
func (AbortEvent) harnessEventTag()       {}
func (SettledEvent) harnessEventTag()     {}
func (ToolsUpdateEvent) harnessEventTag() {}

// The events below also reach wildcard SubscribeHarness listeners (besides their
// typed handlers), mirroring TS emitOwn which feeds every "own" event to the
// wildcard subscribe() listeners.
func (ModelSelectEvent) harnessEventTag()         {}
func (ThinkingLevelSelectEvent) harnessEventTag() {}
func (ResourcesUpdateEvent) harnessEventTag()     {}
func (SessionTreeEvent) harnessEventTag()         {}
func (SessionCompactEvent) harnessEventTag()      {}

func (h *AgentHarness) SubscribeHarness(f func(context.Context, HarnessEvent) error) func() {
	if f == nil {
		return func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextHarnessListenerID
	h.nextHarnessListenerID++
	if h.harnessListeners == nil {
		h.harnessListeners = map[uint64]func(context.Context, HarnessEvent) error{}
	}
	h.harnessListeners[id] = f
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.harnessListeners, id)
	}
}

func (h *AgentHarness) emitHarness(ctx context.Context, ev HarnessEvent) error {
	h.mu.Lock()
	ids := make([]uint64, 0, len(h.harnessListeners))
	for id := range h.harnessListeners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	listeners := make([]func(context.Context, HarnessEvent) error, 0, len(ids))
	for _, id := range ids {
		listeners = append(listeners, h.harnessListeners[id])
	}
	h.dispatching = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.dispatching = false
		h.mu.Unlock()
	}()
	// Harness listeners run without h.mu held. They must not call
	// state-mutating harness methods; those methods panic during dispatch.
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		if err := listener(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

func (h *AgentHarness) queueUpdateEvent() QueueUpdateEvent {
	return QueueUpdateEvent{
		Steer:    h.steerQueue.Snapshot(),
		FollowUp: h.followUpQueue.Snapshot(),
		NextTurn: h.nextTurnQueue.Snapshot(),
	}
}
