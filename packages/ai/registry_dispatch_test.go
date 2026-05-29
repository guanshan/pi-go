package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBuiltInChatAPIProvidersAreRegistered(t *testing.T) {
	RegisterBuiltinProviders()
	apis := []string{
		"faux",
		"anthropic-messages",
		"bedrock-converse-stream",
		"google-generative-ai",
		"google-vertex",
		"mistral-conversations",
		"openai-completions",
		"openai-responses",
		"azure-openai-responses",
		"openai-codex-responses",
	}
	for _, api := range apis {
		if _, ok := ResolveAPIProvider(api); !ok {
			t.Fatalf("expected provider for api %q", api)
		}
	}
}

func TestProviderSourceNamespaceUnregister(t *testing.T) {
	provider := testRegistryProvider{api: "test-api"}
	RegisterProvider(provider, "test-source")
	if got := GetAPIProvider("test-api"); got == nil || got.API() != "test-api" {
		t.Fatalf("provider not registered: %#v", got)
	}
	UnregisterProviders("test-source")
	if got := GetAPIProvider("test-api"); got != nil {
		t.Fatalf("provider should be unregistered: %#v", got)
	}
	RegisterBuiltinProviders()
}

func TestBuiltinRegistrationDoesNotClobberCustomProviderOnPublicCalls(t *testing.T) {
	ResetAPIProviders()
	defer ResetAPIProviders()

	custom := &entryPointProvider{api: "faux", streamText: "custom stream", simpleText: "custom simple"}
	RegisterProvider(custom, "custom-faux")
	model := Model{Provider: "faux", ID: "faux", API: "faux"}

	msg, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if MessageText(msg) != "custom stream" {
		t.Fatalf("Complete used %q", MessageText(msg))
	}
	if got := GetAPIProvider("faux"); got != custom {
		t.Fatalf("custom provider was clobbered after Complete: %#v", got)
	}

	if got := Stream(context.Background(), model, Context{}, StreamOptions{}).Result(); MessageText(got) != "custom stream" {
		t.Fatalf("Stream used %q", MessageText(got))
	}
	if got := GetAPIProvider("faux"); got != custom {
		t.Fatalf("custom provider was clobbered after Stream: %#v", got)
	}

	msg, err = CompleteSimple(context.Background(), model, Context{}, SimpleStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if MessageText(msg) != "custom simple" {
		t.Fatalf("CompleteSimple used %q", MessageText(msg))
	}
	if got := GetAPIProvider("faux"); got != custom {
		t.Fatalf("custom provider was clobbered after CompleteSimple: %#v", got)
	}

	if got := StreamSimple(context.Background(), model, Context{}, SimpleStreamOptions{}).Result(); MessageText(got) != "custom simple" {
		t.Fatalf("StreamSimple used %q", MessageText(got))
	}
	if got := GetAPIProvider("faux"); got != custom {
		t.Fatalf("custom provider was clobbered after StreamSimple: %#v", got)
	}
}

func TestCustomProviderRegistrationIsConsistentAcrossPublicEntrypoints(t *testing.T) {
	ResetAPIProviders()
	defer ResetAPIProviders()

	provider := &entryPointProvider{api: "unit-api", streamText: "stream path", simpleText: "simple path"}
	RegisterProvider(provider, "unit")
	model := Model{Provider: "unit", ID: "model", API: "unit-api"}

	msg, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err != nil || MessageText(msg) != "stream path" {
		t.Fatalf("Complete msg=%#v err=%v", msg, err)
	}
	if msg := Stream(context.Background(), model, Context{}, StreamOptions{}).Result(); MessageText(msg) != "stream path" {
		t.Fatalf("Stream msg=%#v", msg)
	}
	msg, err = CompleteSimple(context.Background(), model, Context{}, SimpleStreamOptions{})
	if err != nil || MessageText(msg) != "simple path" {
		t.Fatalf("CompleteSimple msg=%#v err=%v", msg, err)
	}
	if msg := StreamSimple(context.Background(), model, Context{}, SimpleStreamOptions{}).Result(); MessageText(msg) != "simple path" {
		t.Fatalf("StreamSimple msg=%#v", msg)
	}
}

func TestStreamlessChatUsesProviderCompleteInterface(t *testing.T) {
	ResetAPIProviders()
	defer ResetAPIProviders()

	provider := &completeEntryPointProvider{api: "complete-api", completeText: "complete path", streamText: "stream path"}
	RegisterProvider(provider, "complete")
	model := Model{Provider: "complete", ID: "model", API: "complete-api"}

	msg, err := (&ModelRegistry{}).StreamlessChat(context.Background(), ChatRequest{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if MessageText(msg.Message) != "complete path" {
		t.Fatalf("StreamlessChat used %q", MessageText(msg.Message))
	}
	if provider.completeCalls != 1 || provider.streamCalls != 0 {
		t.Fatalf("completeCalls=%d streamCalls=%d", provider.completeCalls, provider.streamCalls)
	}
}

func TestStreamUsesCallerContext(t *testing.T) {
	ResetAPIProviders()
	defer ResetAPIProviders()

	key := contextKey("trace")
	provider := &contextProvider{api: "ctx-api", valueKey: key}
	RegisterProvider(provider, "ctx")
	model := Model{Provider: "ctx", ID: "model", API: "ctx-api"}

	parentDeadline := time.Now().Add(-time.Millisecond)
	parent, parentCancel := context.WithDeadline(context.WithValue(context.Background(), key, "kept"), parentDeadline)
	defer parentCancel()

	msg, err := Complete(parent, model, Context{}, StreamOptions{})
	if err == nil || msg.StopReason != "aborted" {
		t.Fatalf("deadline msg=%#v err=%v", msg, err)
	}
	if provider.value != "kept" {
		t.Fatalf("ctx value was lost: %#v", provider.value)
	}
	if provider.deadline.IsZero() || !provider.deadline.Equal(parentDeadline) {
		t.Fatalf("deadline=%v want=%v", provider.deadline, parentDeadline)
	}

	provider.value = nil
	provider.deadline = time.Time{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "still-kept"))
	cancel()
	msg, err = Complete(parent, model, Context{}, StreamOptions{})
	if err == nil || msg.StopReason != "aborted" {
		t.Fatalf("cancel msg=%#v err=%v", msg, err)
	}
	if provider.value != "still-kept" {
		t.Fatalf("ctx value was lost after cancel: %#v", provider.value)
	}
}

func TestUnregisteredChatAPIProviderReturnsClearError(t *testing.T) {
	registry := &ModelRegistry{}
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{Provider: "test", ID: "missing", API: "missing-api"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `provider api "missing-api" is not registered`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type testRegistryProvider struct {
	api string
}

func (p testRegistryProvider) API() string { return p.api }

func (p testRegistryProvider) Stream(context.Context, *ModelRegistry, ChatRequest) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream(8)
	msg := NewAssistantMessage("test-api", "test", "test", TextBlocks("ok"), Usage{}, "stop")
	pushAssistantMessage(stream, msg)
	return stream
}

func (p testRegistryProvider) StreamSimple(ctx context.Context, registry *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

type entryPointProvider struct {
	api        string
	streamText string
	simpleText string
}

func (p *entryPointProvider) API() string { return p.api }

func (p *entryPointProvider) Stream(_ context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return testMessageStream(req.Model, p.streamText)
}

func (p *entryPointProvider) StreamSimple(_ context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return testMessageStream(req.Model, p.simpleText)
}

type completeEntryPointProvider struct {
	api           string
	completeText  string
	streamText    string
	completeCalls int
	streamCalls   int
}

func (p *completeEntryPointProvider) API() string { return p.api }

func (p *completeEntryPointProvider) Complete(_ context.Context, _ *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	p.completeCalls++
	msg := NewAssistantMessageForModel(req.Model, TextBlocks(p.completeText), Usage{}, "stop")
	return ChatResponse{Message: msg}, nil
}

func (p *completeEntryPointProvider) Stream(_ context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	p.streamCalls++
	return testMessageStream(req.Model, p.streamText)
}

func (p *completeEntryPointProvider) StreamSimple(ctx context.Context, registry *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

type contextKey string

type contextProvider struct {
	api      string
	valueKey any
	value    any
	deadline time.Time
}

func (p *contextProvider) API() string { return p.api }

func (p *contextProvider) Stream(ctx context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	p.value = ctx.Value(p.valueKey)
	p.deadline, _ = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		stream := NewAssistantMessageEventStream(1)
		msg := NewAssistantMessageForModel(req.Model, nil, Usage{}, "aborted")
		msg.ErrorMessage = err.Error()
		pushAssistantError(stream, msg)
		return stream
	}
	select {
	case <-ctx.Done():
		stream := NewAssistantMessageEventStream(1)
		msg := NewAssistantMessageForModel(req.Model, nil, Usage{}, "aborted")
		msg.ErrorMessage = ctx.Err().Error()
		pushAssistantError(stream, msg)
		return stream
	default:
		return testMessageStream(req.Model, "ok")
	}
}

func (p *contextProvider) StreamSimple(ctx context.Context, registry *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, registry, req)
}

func testMessageStream(model Model, text string) *AssistantMessageEventStream {
	stream := NewAssistantMessageEventStream(8)
	msg := NewAssistantMessageForModel(model, TextBlocks(text), Usage{}, "stop")
	pushAssistantMessage(stream, msg)
	return stream
}
