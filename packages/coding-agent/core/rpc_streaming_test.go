package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

// syncBuffer is a goroutine-safe buffer so the test can read RunRPC's output
// while the RPC read loop / prompt goroutine keep writing to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gatedStreamingProvider streams a single text delta, signals once via started,
// then blocks until release is closed or the context is cancelled. It is safe
// across multiple turns (e.g. after steering) because started is only closed
// once.
type gatedStreamingProvider struct {
	startedOnce sync.Once
	started     chan struct{}
	release     chan struct{}
}

func newGatedStreamingProvider() *gatedStreamingProvider {
	return &gatedStreamingProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *gatedStreamingProvider) API() string { return "coding-agent-streaming-e2e" }

func (p *gatedStreamingProvider) Stream(ctx context.Context, _ *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream(8)
	go func() {
		start := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "")
		partial := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("he"), ai.Usage{}, "")
		final := ai.NewAssistantMessageForModel(req.Model, ai.TextBlocks("hello"), ai.Usage{}, "stop")
		stream.Push(ai.AssistantMessageEvent{Type: "start", Partial: start})
		stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "he", Partial: partial})
		p.startedOnce.Do(func() { close(p.started) })
		select {
		case <-p.release:
		case <-ctx.Done():
			aborted := ai.NewAssistantMessageForModel(req.Model, nil, ai.Usage{}, "aborted")
			aborted.ErrorMessage = ctx.Err().Error()
			stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "aborted", Error: aborted})
			return
		}
		stream.Push(ai.AssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "llo", Partial: final})
		stream.Push(ai.AssistantMessageEvent{Type: "done", Reason: final.StopReason, Message: final})
	}()
	return stream
}

func (p *gatedStreamingProvider) StreamSimple(ctx context.Context, registry *ai.ModelRegistry, req ai.ChatRequest) *ai.AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

func gatedRuntimeFactory(t *testing.T, provider *gatedStreamingProvider) CreateAgentSessionRuntimeFactory {
	t.Helper()
	return func(ctx context.Context, options CreateAgentSessionRuntimeFactoryInput) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(ctx, CreateAgentSessionServicesOptions{
			Cwd:      options.Cwd,
			AgentDir: options.AgentDir,
			ResourceLoaderOptions: DefaultResourceLoaderOptions{
				NoContextFiles:    true,
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
			},
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		model := ai.Model{Provider: "unit", ID: "stream-model", API: provider.API(), ContextWindow: 100000}
		created, err := CreateAgentSessionFromServices(ctx, CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			ScopedModels:   []ScopedModel{{Model: model}},
			NoTools:        NoToolsAll,
		})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: created,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}
}

// TestRunRPCAbortDuringStreaming verifies that the RPC read loop keeps
// processing commands while a prompt is streaming: a prompt is dispatched, and
// an abort sent mid-stream is handled immediately rather than blocking behind
// the in-flight agent run.
func TestRunRPCAbortDuringStreaming(t *testing.T) {
	provider := newGatedStreamingProvider()
	ai.RegisterProvider(provider, "coding-agent-streaming-e2e")
	defer ai.UnregisterProviders("coding-agent-streaming-e2e")

	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions")
	initial, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), gatedRuntimeFactory(t, provider), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- RunRPC(context.Background(), runtime, pr, out)
	}()

	writeLine := func(value map[string]any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := pw.Write(append(data, '\n')); err != nil {
			t.Error(err)
		}
	}

	writeLine(map[string]any{"id": "p1", "type": "prompt", "message": "hello"})

	// Wait until the agent is actually streaming.
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider never started streaming")
	}
	if !runtime.Session().State().IsStreaming {
		t.Fatal("expected agent to be streaming after first delta")
	}

	// Send abort mid-stream. If the read loop were blocked on the prompt, this
	// command would never be processed and RunRPC would never return.
	writeLine(map[string]any{"id": "a1", "type": "abort"})

	// Close stdin so RunRPC returns once in-flight work drains.
	_ = pw.Close()

	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RunRPC returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunRPC did not return; abort was not processed during streaming")
	}

	output := out.String()
	if !strings.Contains(output, `"command":"prompt"`) || !strings.Contains(output, `"id":"p1"`) {
		t.Fatalf("missing prompt success response: %s", output)
	}
	if !strings.Contains(output, `"command":"abort"`) || !strings.Contains(output, `"id":"a1"`) {
		t.Fatalf("missing abort response: %s", output)
	}
	if runtime.Session().State().IsStreaming {
		t.Fatal("expected streaming to stop after abort")
	}
}

// TestRunRPCSteerDuringStreaming verifies a steer command sent during streaming
// is accepted (success response) without requiring the prompt to finish first.
func TestRunRPCSteerDuringStreaming(t *testing.T) {
	provider := newGatedStreamingProvider()
	ai.RegisterProvider(provider, "coding-agent-streaming-e2e")
	defer ai.UnregisterProviders("coding-agent-streaming-e2e")

	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions")
	initial, err := NewSessionManager(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), gatedRuntimeFactory(t, provider), CreateAgentSessionRuntimeOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: initial,
	})
	if err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
	out := &syncBuffer{}
	rpcDone := make(chan error, 1)
	go func() {
		rpcDone <- RunRPC(context.Background(), runtime, pr, out)
	}()

	writeLine := func(value map[string]any) {
		data, _ := json.Marshal(value)
		if _, err := pw.Write(append(data, '\n')); err != nil {
			t.Error(err)
		}
	}

	writeLine(map[string]any{"id": "p1", "type": "prompt", "message": "hello"})
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider never started streaming")
	}

	// A bare prompt while streaming must report streamingBehavior is required.
	writeLine(map[string]any{"id": "p2", "type": "prompt", "message": "again"})
	// A prompt with steer behavior is accepted.
	writeLine(map[string]any{"id": "p3", "type": "prompt", "message": "steered", "streamingBehavior": "steer"})

	// Release the stream and close input.
	close(provider.release)
	_ = pw.Close()

	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("RunRPC returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunRPC did not return")
	}

	output := out.String()
	if !strings.Contains(output, `"id":"p3"`) || !strings.Contains(output, `"success":true`) {
		t.Fatalf("expected steered prompt success: %s", output)
	}
	if !strings.Contains(output, `"id":"p2"`) {
		t.Fatalf("expected response for bare streaming prompt: %s", output)
	}
}
