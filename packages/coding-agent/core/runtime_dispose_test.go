package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

// gatedDisposeProvider drives the agent loop through Stream, which blocks until
// the run context is cancelled, simulating a long-running prompt that only ends
// when the active agent is aborted.
type gatedDisposeProvider struct {
	api     string
	started chan struct{}
	once    sync.Once
}

func (p *gatedDisposeProvider) API() string { return p.api }

func (p *gatedDisposeProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(8)
	go func() {
		start := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: start})
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
		aborted.ErrorMessage = ctx.Err().Error()
		stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
	}()
	return stream
}

func (p *gatedDisposeProvider) StreamSimple(ctx context.Context, reg *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, reg, req)
}

// TestAgentSessionRuntimeDisposeAbortsActiveWorkAndShutsDownOnce verifies that
// runtime.Dispose() runs the full session disposal (mirroring
// agent-session-runtime.ts dispose -> session.dispose): it aborts the in-flight
// agent and any bash/retry/compaction/branch-summary work, runs
// beforeSessionInvalidate, shuts down the extension runtime, and emits
// session_shutdown(quit) exactly once (no double emit).
func TestAgentSessionRuntimeDisposeAbortsActiveWorkAndShutsDownOnce(t *testing.T) {
	provider := &gatedDisposeProvider{api: "runtime-dispose-abort-api", started: make(chan struct{})}
	ai.RegisterProvider(provider, provider.api)
	defer ai.UnregisterProviders(provider.api)

	cwd := t.TempDir()
	agentDir := t.TempDir()
	var events []string
	model := ai.Model{Provider: "unit", ID: "runtime-dispose-model", API: provider.api, MaxOutput: 2048, ContextWindow: 100000}
	factory := func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
				ExtensionFactories: []coreext.Factory{
					func(api *coreext.API) error {
						api.On("session_shutdown", func(payload any) {
							event := payload.(*coreext.SessionShutdownEvent)
							events = append(events, "shutdown:"+string(event.Reason))
						})
						api.OnShutdown(func(context.Context) error {
							events = append(events, "runtime_shutdown")
							return nil
						})
						return nil
					},
				},
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			ScopedModels:   []ScopedModel{{Model: model}},
			Model:          model,
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{CreateAgentSessionResult: created, Services: services}, nil
	}

	runtime, err := CreateAgentSessionRuntime(context.Background(), factory, CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: InMemorySession(cwd),
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidations := 0
	runtime.SetBeforeSessionInvalidate(func() { invalidations++ })

	session := runtime.Session()

	// Track the other abort hooks the session must cancel on disposal. These
	// mirror abortRetry/abortCompaction/abortBranchSummary/abortBash in
	// agent-session.ts dispose().
	var retryCanceled, compactionCanceled, branchCanceled, bashCanceled atomic.Bool
	session.mu.Lock()
	session.retryCancel = func() { retryCanceled.Store(true) }
	session.compactionCancel = func() { compactionCanceled.Store(true) }
	session.branchSummaryCancel = func() { branchCanceled.Store(true) }
	session.activeBashCancel = func() { bashCanceled.Store(true) }
	session.mu.Unlock()

	// Drive a streaming prompt so the session has a live active agent. The gated
	// provider blocks in Stream until the run context is cancelled, so this
	// goroutine only returns once the active agent is aborted by disposal.
	promptDone := make(chan error, 1)
	go func() {
		promptDone <- session.Prompt(context.Background(), "keep streaming", nil, nil)
	}()
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider never started streaming")
	}

	if err := runtime.Dispose(context.Background()); err != nil {
		t.Fatalf("dispose: %v", err)
	}

	// The in-flight agent must have been aborted by disposal.
	select {
	case <-promptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("dispose did not abort the active agent")
	}

	if !bashCanceled.Load() {
		t.Error("dispose did not call AbortBash")
	}
	if !retryCanceled.Load() {
		t.Error("dispose did not abort retry")
	}
	if !compactionCanceled.Load() {
		t.Error("dispose did not abort compaction")
	}
	if !branchCanceled.Load() {
		t.Error("dispose did not abort branch summary")
	}
	if invalidations != 1 {
		t.Errorf("beforeSessionInvalidate ran %d times, want 1", invalidations)
	}

	shutdowns := 0
	runtimeShutdowns := 0
	for _, ev := range events {
		switch ev {
		case "shutdown:quit":
			shutdowns++
		case "runtime_shutdown":
			runtimeShutdowns++
		}
	}
	if shutdowns != 1 {
		t.Errorf("session_shutdown(quit) emitted %d times, want exactly 1; events=%#v", shutdowns, events)
	}
	if runtimeShutdowns != 1 {
		t.Errorf("extension runtime shutdown ran %d times, want 1; events=%#v", runtimeShutdowns, events)
	}

	// The runtime must release its references after disposal.
	if runtime.Session() != nil || runtime.Services() != nil {
		t.Errorf("runtime not disposed: session=%v services=%v", runtime.Session(), runtime.Services())
	}
}
