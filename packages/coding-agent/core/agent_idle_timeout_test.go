package core

import (
	"context"
	"sync"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// idleCapturingProvider records the IdleTimeoutMs seen on each ChatRequest so the
// test can assert the value propagated from settings into the provider request.
type idleCapturingProvider struct {
	mu       sync.Mutex
	lastIdle int
	captured bool
}

func (p *idleCapturingProvider) API() string { return "coding-agent-idle-timeout" }

func (p *idleCapturingProvider) Stream(_ context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	p.mu.Lock()
	p.lastIdle = req.IdleTimeoutMs
	p.captured = true
	p.mu.Unlock()
	stream := ai.NewAssistantMessageEventStream(4)
	go func() {
		final := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("ok"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: "stop", Message: final})
	}()
	return stream
}

func (p *idleCapturingProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

// TestIdleTimeoutPropagatesFromSettings verifies the configured HTTP idle
// timeout (SettingsManager.HTTPIdleTimeoutMS) is wired onto the provider request
// via ai.ChatRequest.IdleTimeoutMs (P1-08).
func TestIdleTimeoutPropagatesFromSettings(t *testing.T) {
	provider := &idleCapturingProvider{}
	ai.RegisterProvider(provider, provider.API())
	defer ai.UnregisterProviders(provider.API())

	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	idle := HTTPIdleTimeoutSetting(42000)
	settings.Project.HTTPIdleTimeoutMS = &idle
	if settings.HTTPIdleTimeoutMS() != 42000 {
		t.Fatalf("settings idle timeout=%d, want 42000", settings.HTTPIdleTimeoutMS())
	}

	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	model := ai.Model{Provider: "unit", ID: "idle-model", API: provider.API()}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")

	if err := agent.Prompt(context.Background(), "hello", nil, func(ai.Event) {}); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	provider.mu.Lock()
	captured, idleMs := provider.captured, provider.lastIdle
	provider.mu.Unlock()
	if !captured {
		t.Fatalf("provider was never invoked")
	}
	if idleMs != 42000 {
		t.Fatalf("ChatRequest.IdleTimeoutMs=%d, want 42000 (from settings)", idleMs)
	}
}
