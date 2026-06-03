package core

import (
	"context"
	"sync"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// capturingFauxProvider overrides the builtin "faux" API provider so the test
// can inspect the ChatRequest the agent loop hands to the provider. The agent
// builds the ChatRequest from the AgentOptions (SessionID/Transport/
// ThinkingBudgets/provider timeout and retry settings) populated in newLoopAgent, so capturing it
// proves those fields are actually wired through.
type capturingFauxProvider struct {
	mu       sync.Mutex
	captured *ai.ChatRequest
}

func (*capturingFauxProvider) API() string { return "faux" }

func (p *capturingFauxProvider) record(req ai.ChatRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	clone := req
	p.captured = &clone
}

func (p *capturingFauxProvider) request() *ai.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captured
}

func (p *capturingFauxProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	p.record(req)
	stream := ai.NewAssistantMessageEventStream(1)
	msg := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
	stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: msg})
	stream.End(msg)
	return stream
}

func (p *capturingFauxProvider) StreamSimple(ctx context.Context, r *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

// TestNewLoopAgentWiresSessionAndProviderSettings is the parity guard for
// finding P1-A1 / P1-1: coding-agent must forward SessionID, Transport,
// ThinkingBudgets and provider timeout/retry controls into the agent (mirrors
// sdk.ts:383,391-393). Before the fix these were never populated, so the
// prompt-cache key, session-affinity header, transport selection and provider
// retry behavior silently had no effect.
func TestNewLoopAgentWiresSessionAndProviderSettings(t *testing.T) {
	capture := &capturingFauxProvider{}
	ai.RegisterProvider(capture, "test-capturing-faux")
	t.Cleanup(ai.ResetAPIProviders)

	cwd := t.TempDir()
	settings := &SettingsManager{
		CWD:      cwd,
		AgentDir: t.TempDir(),
		Global: Settings{
			Transport:       "sse",
			ThinkingBudgets: ThinkingBudgets{Minimal: 111, Low: 222, Medium: 333, High: 444},
			Retry:           RetryConfig{Provider: ProviderRetryConfig{TimeoutMS: 2345, MaxRetries: 6, MaxRetryDelayMS: 12345}},
		},
	}
	idle := HTTPIdleTimeoutSetting(45678)
	settings.Global.HTTPIdleTimeoutMS = &idle

	auth := ai.NewAuthStorage(settings.AgentDir)
	registry := ai.NewModelRegistry(settings.AgentDir, auth)
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}

	session := InMemorySession(cwd)
	session.Header.ID = "wiring-session-id"

	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agentSession := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingHigh, ToolSet{}, "system")

	if err := agentSession.Prompt(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}

	req := capture.request()
	if req == nil {
		t.Fatal("provider never received a ChatRequest")
	}
	if req.SessionID != "wiring-session-id" {
		t.Fatalf("SessionID=%q, want wiring-session-id", req.SessionID)
	}
	if req.Transport != "sse" {
		t.Fatalf("Transport=%q, want sse", req.Transport)
	}
	if req.TimeoutMs != 2345 {
		t.Fatalf("TimeoutMs=%d, want 2345", req.TimeoutMs)
	}
	if req.IdleTimeoutMs != 45678 {
		t.Fatalf("IdleTimeoutMs=%d, want 45678", req.IdleTimeoutMs)
	}
	if req.MaxRetries != 6 {
		t.Fatalf("MaxRetries=%d, want 6", req.MaxRetries)
	}
	if req.MaxRetryDelayMs != 12345 {
		t.Fatalf("MaxRetryDelayMs=%d, want 12345", req.MaxRetryDelayMs)
	}
	want := ai.ThinkingBudgets{Minimal: 111, Low: 222, Medium: 333, High: 444}
	if req.ThinkingBudgets != want {
		t.Fatalf("ThinkingBudgets=%+v, want %+v", req.ThinkingBudgets, want)
	}
}

// TestSettingsManagerThinkingBudgets covers the new getter (mirrors
// settings-manager.ts:926-928 getThinkingBudgets), including the project-over-
// global deep merge and the nil result when no budgets are configured.
func TestSettingsManagerThinkingBudgets(t *testing.T) {
	empty := &SettingsManager{}
	if got := empty.ThinkingBudgets(); got != nil {
		t.Fatalf("ThinkingBudgets()=%+v, want nil when unset", got)
	}

	settings := &SettingsManager{
		Global:  Settings{ThinkingBudgets: ThinkingBudgets{Minimal: 100, Low: 200, Medium: 300, High: 400}},
		Project: Settings{ThinkingBudgets: ThinkingBudgets{Medium: 999}},
	}
	got := settings.ThinkingBudgets()
	if got == nil {
		t.Fatal("ThinkingBudgets()=nil, want merged budgets")
	}
	want := ai.ThinkingBudgets{Minimal: 100, Low: 200, Medium: 999, High: 400}
	if *got != want {
		t.Fatalf("ThinkingBudgets()=%+v, want %+v", *got, want)
	}
}
