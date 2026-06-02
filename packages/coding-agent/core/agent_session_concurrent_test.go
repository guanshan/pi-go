package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

// blockingStreamProvider keeps the first prompt's stream open until release is
// closed, so a second concurrent prompt is guaranteed to observe the streaming
// guard. Gating is channel-based (not wall-clock) to keep the test
// deterministic. It mirrors the abort-aware mock streamFn in the TypeScript
// test packages/coding-agent/test/agent-session-concurrent.test.ts:93-108.
type blockingStreamProvider struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingStreamProvider() *blockingStreamProvider {
	return &blockingStreamProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingStreamProvider) API() string { return "coding-agent-concurrent-guard" }

func (p *blockingStreamProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(8)
	go func() {
		start := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: start})
		// Signal exactly once that the first stream is in flight.
		select {
		case <-p.started:
		default:
			close(p.started)
		}
		select {
		case <-p.release:
		case <-ctx.Done():
			aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
			aborted.ErrorMessage = ctx.Err().Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
			return
		}
		final := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("done"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: final.StopReason, Message: final})
	}()
	return stream
}

func (p *blockingStreamProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

func newConcurrentGuardSession(t *testing.T, provider *blockingStreamProvider) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "concurrent-model", API: provider.API()}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	return NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}

// TestAgentSessionConcurrentPromptIsRejected mirrors the TypeScript
// "should throw when prompt() called while streaming" case
// (packages/coding-agent/test/agent-session-concurrent.test.ts:130-150):
// while one prompt is streaming, a second plain Prompt is rejected with the
// busy error that advises using steer or follow_up.
func TestAgentSessionConcurrentPromptIsRejected(t *testing.T) {
	provider := newBlockingStreamProvider()
	ai.RegisterProvider(provider, provider.API())
	defer ai.UnregisterProviders(provider.API())

	agent := newConcurrentGuardSession(t, provider)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- agent.Prompt(context.Background(), "First message", nil, nil)
	}()

	// Wait for the first stream to be in flight (channel-gated, not wall-clock).
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt never started streaming")
	}

	// A second concurrent Prompt must be rejected with the busy error.
	err := agent.Prompt(context.Background(), "Second message", nil, nil)
	if err == nil {
		t.Fatal("expected second concurrent prompt to be rejected while streaming")
	}
	if !isAlreadyStreamingPromptError(err) {
		t.Fatalf("expected already-streaming busy error, got %v", err)
	}
	if !strings.Contains(err.Error(), "steer") || !strings.Contains(err.Error(), "follow_up") {
		t.Fatalf("busy error should advise steer/follow_up, got %q", err.Error())
	}

	// Release the first stream and let it finish cleanly.
	close(provider.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first prompt failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt did not finish after release")
	}
}

// TestAgentSessionConcurrentSteerIsQueued mirrors the TypeScript
// "should allow steer() while streaming" case
// (packages/coding-agent/test/agent-session-concurrent.test.ts:152-166):
// the steer path advised by the busy error is accepted (queued) instead of
// rejected while the agent is streaming.
func TestAgentSessionConcurrentSteerIsQueued(t *testing.T) {
	provider := newBlockingStreamProvider()
	ai.RegisterProvider(provider, provider.API())
	defer ai.UnregisterProviders(provider.API())

	agent := newConcurrentGuardSession(t, provider)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- agent.Prompt(context.Background(), "First message", nil, nil)
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt never started streaming")
	}

	// Send with steer behavior is the advised path; preflight should report
	// success and the call should queue without error rather than reject.
	preflight := make(chan bool, 1)
	steerErr := agent.Send(context.Background(), "Steering message", nil, StreamingSteer, func(success bool) {
		preflight <- success
	}, nil)
	if steerErr != nil {
		t.Fatalf("steer while streaming should not error, got %v", steerErr)
	}
	select {
	case success := <-preflight:
		if !success {
			t.Fatal("steer preflight should report success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("steer preflight never fired")
	}

	close(provider.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first prompt failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt did not finish after release")
	}
}

// TestAgentSessionConcurrentFollowUpIsQueued mirrors the TypeScript
// "should allow followUp() while streaming" case
// (packages/coding-agent/test/agent-session-concurrent.test.ts:168-182):
// the follow_up path advised by the busy error is accepted (queued) while the
// agent is streaming, and the queued message becomes observable.
func TestAgentSessionConcurrentFollowUpIsQueued(t *testing.T) {
	provider := newBlockingStreamProvider()
	ai.RegisterProvider(provider, provider.API())
	defer ai.UnregisterProviders(provider.API())

	agent := newConcurrentGuardSession(t, provider)

	queued := make(chan []string, 4)
	unsubscribe := agent.Subscribe(func(event SessionEvent) {
		if update, ok := event.(QueueUpdateEvent); ok {
			queued <- update.FollowUp
		}
	})
	defer unsubscribe()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- agent.Prompt(context.Background(), "First message", nil, nil)
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt never started streaming")
	}

	preflight := make(chan bool, 1)
	followUpErr := agent.Send(context.Background(), "Follow-up message", nil, StreamingFollowUp, func(success bool) {
		preflight <- success
	}, nil)
	if followUpErr != nil {
		t.Fatalf("follow_up while streaming should not error, got %v", followUpErr)
	}
	select {
	case success := <-preflight:
		if !success {
			t.Fatal("follow_up preflight should report success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow_up preflight never fired")
	}

	// The follow-up should be observable in a queue_update event.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-queued:
			if containsString(update, "Follow-up message") {
				goto released
			}
		case <-deadline:
			t.Fatal("follow-up message never appeared in queue update")
		}
	}

released:
	close(provider.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first prompt failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt did not finish after release")
	}
}
