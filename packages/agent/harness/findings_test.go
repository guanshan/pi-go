package harness

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// flakyStorage wraps a MemoryStorage and fails AppendEntry on demand, allowing
// tests to inject session-write failures deterministically.
type flakyStorage struct {
	*session.MemoryStorage
	mu       sync.Mutex
	failErr  error
	failNext bool
	// failOnCall, when > 0, fails the AppendEntry call with that 1-based index
	// (counting only calls observed by this wrapper).
	failOnCall int
	appends    int
}

func newFlakyStorage(t *testing.T) *flakyStorage {
	t.Helper()
	mem, err := session.NewMemoryStorage(session.Metadata{ID: "s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &flakyStorage{MemoryStorage: mem, failErr: errors.New("storage offline")}
}

func (s *flakyStorage) arm() {
	s.mu.Lock()
	s.failNext = true
	s.mu.Unlock()
}

func (s *flakyStorage) recover() {
	s.mu.Lock()
	s.failNext = false
	s.failOnCall = 0
	s.mu.Unlock()
}

func (s *flakyStorage) AppendEntry(ctx context.Context, entry session.Entry) error {
	s.mu.Lock()
	s.appends++
	if s.failNext || (s.failOnCall > 0 && s.appends == s.failOnCall) {
		s.mu.Unlock()
		return s.failErr
	}
	s.mu.Unlock()
	return s.MemoryStorage.AppendEntry(ctx, entry)
}

// TestFlushPendingWritesRetainsFailedAndRemaining verifies R3-P1-2: a failed
// flush must leave the failed write and every later write queued in order so a
// later flush can write them all.
func TestFlushPendingWritesRetainsFailedAndRemaining(t *testing.T) {
	ctx := context.Background()
	storage := newFlakyStorage(t)
	sess := session.New(storage)
	h, err := New(Options{Session: sess, Model: ai.Model{Provider: "test", ID: "m", API: "test"}})
	if err != nil {
		t.Fatal(err)
	}

	// Queue three writes directly so the head succeeds, the 2nd fails.
	first := pendingCustomWrite{CustomType: "a", Data: "1"}
	second := pendingCustomWrite{CustomType: "b", Data: "2"}
	third := pendingCustomWrite{CustomType: "c", Data: "3"}
	h.mu.Lock()
	h.pendingWrites = []pendingSessionWrite{first, second, third}
	h.mu.Unlock()

	// Fail precisely on the second AppendEntry so the first write commits and
	// the second + third remain queued.
	storage.mu.Lock()
	storage.failOnCall = 2
	storage.mu.Unlock()

	if err := h.flushPendingSessionWrites(ctx); err == nil {
		t.Fatal("expected flush to fail on the second write")
	}

	h.mu.Lock()
	remaining := append([]pendingSessionWrite(nil), h.pendingWrites...)
	h.mu.Unlock()
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining writes, got %d: %#v", len(remaining), remaining)
	}
	if remaining[0] != pendingSessionWrite(second) || remaining[1] != pendingSessionWrite(third) {
		t.Fatalf("remaining writes not in original order: %#v", remaining)
	}

	// Recover storage and flush again: all remaining writes succeed in order.
	storage.recover()
	if err := h.flushPendingSessionWrites(ctx); err != nil {
		t.Fatalf("flush after recovery failed: %v", err)
	}
	h.mu.Lock()
	leftover := len(h.pendingWrites)
	h.mu.Unlock()
	if leftover != 0 {
		t.Fatalf("expected empty queue after recovery, got %d", leftover)
	}
	customs, err := storage.FindEntries(ctx, "custom")
	if err != nil {
		t.Fatal(err)
	}
	if len(customs) != 3 {
		t.Fatalf("expected 3 custom entries written, got %d", len(customs))
	}
}

// TestIdleSettersPersistBeforeCommit verifies R3-P1-3: when idle, the setters
// validate and persist before committing in-memory state. On write failure the
// getter still returns the old value, no partial entry is written, and a later
// re-set retries the write.
func TestIdleSettersPersistBeforeCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("model", func(t *testing.T) {
		storage := newFlakyStorage(t)
		sess := session.New(storage)
		old := ai.Model{Provider: "test", ID: "old", API: "test"}
		h, err := New(Options{Session: sess, Model: old})
		if err != nil {
			t.Fatal(err)
		}
		next := ai.Model{Provider: "test", ID: "new", API: "test"}
		storage.arm()
		if err := h.SetModel(ctx, next); err == nil {
			t.Fatal("expected SetModel to fail")
		}
		if got := h.GetModel(); got.ID != "old" {
			t.Fatalf("model advanced despite write failure: %#v", got)
		}
		if entries, _ := storage.FindEntries(ctx, "model_change"); len(entries) != 0 {
			t.Fatalf("partial model_change entry written: %#v", entries)
		}
		storage.recover()
		if err := h.SetModel(ctx, next); err != nil {
			t.Fatalf("re-set failed: %v", err)
		}
		if got := h.GetModel(); got.ID != "new" {
			t.Fatalf("model not committed after retry: %#v", got)
		}
		if entries, _ := storage.FindEntries(ctx, "model_change"); len(entries) != 1 {
			t.Fatalf("expected exactly one model_change entry, got %#v", entries)
		}
	})

	t.Run("thinking", func(t *testing.T) {
		storage := newFlakyStorage(t)
		sess := session.New(storage)
		h, err := New(Options{Session: sess, ThinkingLevel: ai.ThinkingLow})
		if err != nil {
			t.Fatal(err)
		}
		storage.arm()
		if err := h.SetThinkingLevel(ctx, ai.ThinkingHigh); err == nil {
			t.Fatal("expected SetThinkingLevel to fail")
		}
		if got := h.GetThinkingLevel(); got != ai.ThinkingLow {
			t.Fatalf("thinking level advanced despite write failure: %v", got)
		}
		if entries, _ := storage.FindEntries(ctx, "thinking_level_change"); len(entries) != 0 {
			t.Fatalf("partial thinking_level_change entry written: %#v", entries)
		}
		storage.recover()
		if err := h.SetThinkingLevel(ctx, ai.ThinkingHigh); err != nil {
			t.Fatalf("re-set failed: %v", err)
		}
		if got := h.GetThinkingLevel(); got != ai.ThinkingHigh {
			t.Fatalf("thinking level not committed after retry: %v", got)
		}
	})

	t.Run("active_tools", func(t *testing.T) {
		storage := newFlakyStorage(t)
		sess := session.New(storage)
		h, err := New(Options{
			Session: sess,
			Model:   ai.Model{Provider: "test", ID: "m", API: "test"},
			Tools:   []agent.AgentTool{namedHarnessTool{name: "alpha"}, namedHarnessTool{name: "beta"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		before := toolNames(h.GetActiveTools())
		storage.arm()
		if err := h.SetActiveTools(ctx, []string{"beta"}); err == nil {
			t.Fatal("expected SetActiveTools to fail")
		}
		if got := toolNames(h.GetActiveTools()); !equalStrings(got, before) {
			t.Fatalf("active tools advanced despite write failure: %#v", got)
		}
		if entries, _ := storage.FindEntries(ctx, "active_tools_change"); len(entries) != 0 {
			t.Fatalf("partial active_tools_change entry written: %#v", entries)
		}
		storage.recover()
		if err := h.SetActiveTools(ctx, []string{"beta"}); err != nil {
			t.Fatalf("re-set failed: %v", err)
		}
		if got := toolNames(h.GetActiveTools()); len(got) != 1 || got[0] != "beta" {
			t.Fatalf("active tools not committed after retry: %#v", got)
		}
	})
}

func toolNames(tools []agent.AgentTool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name())
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDefaultSystemPromptWhenUnset verifies R3-P2-6: with no configured system
// prompt the captured request system prompt defaults to DefaultSystemPrompt,
// while an explicit empty prompt is preserved.
func TestDefaultSystemPromptWhenUnset(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}

	capture := func(opts Options) string {
		var captured string
		opts.Model = model
		opts.StreamFn = func(ctx context.Context, model ai.Model, aiCtx ai.Context, o ai.StreamOptions) agent.AssistantStream {
			captured = aiCtx.SystemPrompt
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		}
		h, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.Prompt(ctx, "hi", PromptOptions{}); err != nil {
			t.Fatal(err)
		}
		return captured
	}

	if got := capture(Options{}); got != DefaultSystemPrompt {
		t.Fatalf("unset system prompt = %q, want %q", got, DefaultSystemPrompt)
	}
	if got := capture(Options{SystemPrompt: StaticSystemPrompt("")}); got != "" {
		t.Fatalf("explicit empty system prompt = %q, want empty", got)
	}
	if got := capture(Options{SystemPrompt: StaticSystemPrompt("custom")}); got != "custom" {
		t.Fatalf("explicit system prompt = %q, want %q", got, "custom")
	}
}

// TestSkillResolvesFromTurnSnapshot verifies R3-P2-1: Skill resolves the skill
// from the same turn state used for the run, so a resource change between the
// lookup and the prompt cannot make them diverge.
func TestSkillResolvesFromTurnSnapshot(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	var captured string
	h, err := New(Options{
		Model: model,
		Resources: Resources{
			Skills: []Skill{{Name: "review", Content: "snapshot content", FilePath: "/skills/review/SKILL.md"}},
		},
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			captured = ai.MessageText(aiCtx.Messages[len(aiCtx.Messages)-1])
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mutate resources from a before_agent_start hook (after the turn snapshot
	// was taken) to prove the prompt uses the snapshot, not live resources.
	h.OnBeforeAgentStart(func(ctx context.Context, ev BeforeAgentStartEvent) (*BeforeAgentStartResult, error) {
		_ = h.SetResources(context.Background(), Resources{
			Skills: []Skill{{Name: "review", Content: "mutated content", FilePath: "/skills/review/SKILL.md"}},
		})
		return nil, nil
	})
	if _, err := h.Skill(ctx, "review", "extra"); err != nil {
		t.Fatal(err)
	}
	if !contains(captured, "snapshot content") {
		t.Fatalf("skill prompt did not use turn snapshot: %q", captured)
	}
	if contains(captured, "mutated content") {
		t.Fatalf("skill prompt leaked mutated resources: %q", captured)
	}
}

// TestSkillReturnsBusyRatherThanNotFound verifies R3-P2-1: when the harness is
// busy, Skill returns a busy error rather than "resource not found".
func TestSkillReturnsBusyRatherThanNotFound(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	started := make(chan struct{})
	release := make(chan struct{})
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			stream := ai.NewAssistantMessageEventStream(4)
			close(started)
			go func() {
				<-release
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = h.Prompt(ctx, "main", PromptOptions{})
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not start")
	}
	// "missing" is not a real skill; busy must take precedence over not-found.
	_, err = h.Skill(ctx, "missing", "")
	var agentErr *agent.AgentError
	if !errors.As(err, &agentErr) || agentErr.Code != agent.AgentErrBusy {
		t.Fatalf("expected busy error, got %#v", err)
	}
	close(release)
	<-done
}

// TestPromptFromAgentEndListenerPreservesNewRun verifies R3-P1-5: a second
// prompt started from an agent_end listener keeps a usable, non-clobbered run,
// and the new run's abort handle survives the old run's release.
func TestPromptFromAgentEndListenerPreservesNewRun(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	calls := 0
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			calls++
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
				stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
				stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var reentered int32
	h.Subscribe(func(ctx context.Context, ev agent.AgentEvent) error {
		if _, ok := ev.(agent.AgentEndEvent); ok {
			if atomic.AddInt32(&reentered, 1) == 1 {
				if _, err := h.Prompt(context.Background(), "nested", PromptOptions{}); err != nil {
					t.Errorf("nested prompt failed: %v", err)
				}
			}
		}
		return nil
	})
	if _, err := h.Prompt(ctx, "main", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 stream calls (outer + nested), got %d", calls)
	}
	// After both runs unwind, the harness must be idle and immediately usable.
	if err := h.WaitForIdle(ctx); err != nil {
		t.Fatalf("WaitForIdle: %v", err)
	}
	if _, err := h.Prompt(ctx, "again", PromptOptions{}); err != nil {
		t.Fatalf("harness not usable after reentrant run: %v", err)
	}
}

// TestReentrantRunAbortHandleSurvivesOldRelease verifies the core of R3-P1-5:
// a run started from an agent_end listener that is still in-flight when the old
// run's release fires must keep its abort handle (release must not clobber it).
func TestReentrantRunAbortHandleSurvivesOldRelease(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	nestedStarted := make(chan struct{})
	var nestedStartOnce sync.Once
	calls := int32(0)
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			n := atomic.AddInt32(&calls, 1)
			stream := ai.NewAssistantMessageEventStream(4)
			if n == 1 {
				// Outer run: complete immediately.
				go func() {
					msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("outer"), ai.Usage{}, "stop")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
				}()
				return stream
			}
			// Nested run: block until aborted so it stays in-flight while the
			// outer run's release runs.
			nestedStartOnce.Do(func() { close(nestedStarted) })
			go func() {
				<-ctx.Done()
				msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
				stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg})
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	nestedDone := make(chan error, 1)
	var once sync.Once
	h.Subscribe(func(ctx context.Context, ev agent.AgentEvent) error {
		if _, ok := ev.(agent.AgentEndEvent); ok {
			once.Do(func() {
				go func() {
					_, err := h.Prompt(context.Background(), "nested", PromptOptions{})
					nestedDone <- err
				}()
				// Wait for the nested run to publish its abort handle before
				// returning, so the outer run's release races against it.
				<-nestedStarted
			})
		}
		return nil
	})
	if _, err := h.Prompt(ctx, "outer", PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	// The outer run has fully unwound. The nested run is still in-flight; its
	// abort handle must survive so Abort cancels it.
	if _, err := h.Abort(ctx); err != nil {
		t.Fatalf("abort: %v", err)
	}
	select {
	case err := <-nestedDone:
		if err != nil {
			t.Fatalf("nested prompt: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nested run was not aborted: its abort handle was clobbered by the old run's release")
	}
}

// TestConcurrentPromptAndAbort exercises the prompt/abort race under -race.
func TestConcurrentPromptAndAbort(t *testing.T) {
	ctx := context.Background()
	model := ai.Model{Provider: "test", ID: "m", API: "test"}
	h, err := New(Options{
		Model: model,
		StreamFn: func(ctx context.Context, model ai.Model, aiCtx ai.Context, opts ai.StreamOptions) agent.AssistantStream {
			stream := ai.NewAssistantMessageEventStream(4)
			go func() {
				select {
				case <-ctx.Done():
					msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "aborted")
					stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: msg})
				case <-time.After(2 * time.Millisecond):
					msg := ai.NewAssistantMessageForModel(model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
					stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
					stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
				}
			}()
			return stream
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.Prompt(ctx, "main", PromptOptions{})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.Abort(ctx)
		}()
		// Serialize each prompt to keep one run at a time, but interleave aborts.
		wg.Wait()
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
