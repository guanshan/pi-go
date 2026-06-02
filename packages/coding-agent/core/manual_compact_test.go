package core

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

// gatedCompactProvider drives the agent loop through Stream (which blocks until
// the context is cancelled, simulating a long-running prompt) and the
// compaction summarizer through StreamSimple (which returns immediately).
type gatedCompactProvider struct {
	api     string
	started chan struct{}
	once    sync.Once
}

func (p *gatedCompactProvider) API() string { return p.api }

func (p *gatedCompactProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(8)
	go func() {
		start := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")
		partial := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("wo"), ai.Usage{}, "")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: start})
		stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "wo", Partial: partial})
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
		aborted.ErrorMessage = ctx.Err().Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
	}()
	return stream
}

func (p *gatedCompactProvider) StreamSimple(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(2)
	go func() {
		if err := ctx.Err(); err != nil {
			aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
			aborted.ErrorMessage = err.Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
			return
		}
		msg := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("compacted summary"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: msg.StopReason, Message: msg})
	}()
	return stream
}

func newManualCompactAgent(t *testing.T, provider ai.Provider) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	settings.Global.Compaction.KeepRecentTokens = 1
	settings.Global.Compaction.ReserveTokens = 64
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "manual-compact-model", API: provider.API(), MaxOutput: 2048, ContextWindow: 100000}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}

	appendSessionMessage(t, session, ai.NewUserMessage("inspect old code", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("looked at it"), ai.Usage{}, "stop"))
	appendSessionMessage(t, session, ai.NewUserMessage("now keep working", nil))
	appendSessionMessage(t, session, ai.NewAssistantMessageForModel(model, ai.TextBlocks("latest response"), ai.Usage{}, "stop"))

	return NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}

// TestManualCompactAbortsActiveAgentFirst verifies that a manual compaction
// aborts the in-flight prompt before taking over the session, so the streaming
// prompt cannot interleave session entries with the compaction (mirrors the TS
// AgentSession.compact() abort-first ordering).
func TestManualCompactAbortsActiveAgentFirst(t *testing.T) {
	provider := &gatedCompactProvider{api: "manual-compact-abort-api", started: make(chan struct{})}
	ai.RegisterProvider(provider, provider.api)
	defer ai.UnregisterProviders(provider.api)

	agent := newManualCompactAgent(t, provider)

	promptDone := make(chan error, 1)
	go func() {
		promptDone <- agent.Prompt(context.Background(), "keep streaming", nil, nil)
	}()

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider never started streaming")
	}
	if !agent.State().IsStreaming {
		t.Fatal("expected agent to be streaming before manual compaction")
	}

	// CompactWithContext must abort the streaming prompt first; if it did not,
	// the gated Stream would block forever and this call would hang.
	result, err := agent.CompactWithContext(context.Background(), "preserve nothing", nil)
	if err != nil {
		t.Fatalf("manual compact: %v", err)
	}

	select {
	case err := <-promptDone:
		_ = err // the prompt is expected to end via abort
	case <-time.After(2 * time.Second):
		t.Fatal("streaming prompt was not aborted by manual compaction")
	}

	if agent.State().IsStreaming {
		t.Fatal("expected streaming to stop after manual compaction")
	}
	if summary, _ := result["summary"].(string); summary == "" {
		t.Fatalf("expected a compaction summary, got %#v", result)
	}
	// The compaction entry must be the final session entry: the aborted prompt
	// entry (if any) is flushed before compaction, never interleaved after it.
	last := agent.Session.Entries[len(agent.Session.Entries)-1]
	if last.Type != "compaction" {
		t.Fatalf("expected last entry to be compaction, got %q", last.Type)
	}
}

// TestManualCompactWithCanceledContext verifies the caller context cancels the
// manual compaction: a pre-canceled context yields an error and no compaction
// entry is appended.
func TestManualCompactWithCanceledContext(t *testing.T) {
	provider := &gatedCompactProvider{api: "manual-compact-cancel-api", started: make(chan struct{})}
	ai.RegisterProvider(provider, provider.api)
	defer ai.UnregisterProviders(provider.api)

	agent := newManualCompactAgent(t, provider)
	before := len(agent.Session.Entries)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := agent.CompactWithContext(ctx, "", nil); err == nil {
		t.Fatal("expected canceled context to fail manual compaction")
	}
	if got := len(agent.Session.Entries); got != before {
		t.Fatalf("compaction entry appended despite cancellation: before=%d after=%d", before, got)
	}
	last := agent.Session.Entries[len(agent.Session.Entries)-1]
	if last.Type == "compaction" {
		t.Fatal("no compaction entry should be appended for a canceled context")
	}
}
