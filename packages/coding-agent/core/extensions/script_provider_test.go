package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestScriptExtensionRegisterProviderBridge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "provider-ext.mjs")
	source := `
export default function (pi) {
	pi.registerProvider({
		api: "script-provider-test",
		complete(req, ctx) {
			return {
				content: [{ type: "text", text: "complete:" + req.systemPrompt + ":" + req.messages[0].content[0].text + ":" + ctx.cwd }],
				usage: { input: req.messages.length, output: 1, totalTokens: req.messages.length + 1 },
				stopReason: "stop",
				responseId: "resp_complete",
			};
		},
		async *stream(req) {
			yield "stream:";
			yield req.messages[0].content[0].text;
		},
		completeSimple(req) {
			return "simple:" + req.messages.length;
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := NewRunnerWithAPI(NewAPI())
	runtime.SetContextProvider(func() ExtensionContextSnapshot {
		return ExtensionContextSnapshot{CWD: dir, Mode: "tui", HasUI: true, IsIdle: true}
	})
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
		ai.UnregisterProviders(ext)
		ai.RegisterBuiltinProviders()
	})
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if providers := runtime.RegisteredProviders(); len(providers) != 1 || providers[0].API != "script-provider-test" || providers[0].Source != ext {
		t.Fatalf("registered providers=%#v", providers)
	}
	provider, ok := ai.ResolveAPIProvider("script-provider-test")
	if !ok {
		t.Fatal("script provider not registered in ai registry")
	}

	model := ai.Model{Provider: "script", ID: "model", API: "script-provider-test"}
	contextMessages := ai.Context{SystemPrompt: "sys", Messages: []ai.Message{ai.NewUserMessage("hi", nil)}}
	streamed, err := ai.Complete(context.Background(), model, contextMessages, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(streamed); got != "stream:hi" {
		t.Fatalf("stream text=%q", got)
	}

	completing, ok := provider.(ai.CompletingProvider)
	if !ok {
		t.Fatal("script provider does not expose Complete")
	}
	response, err := completing.Complete(context.Background(), nil, ai.ChatRequest{
		Model:        model,
		SystemPrompt: "sys",
		Messages:     []ai.Message{ai.NewUserMessage("hi", nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(response.Message); !strings.Contains(got, "complete:sys:hi:"+dir) {
		t.Fatalf("complete text=%q", got)
	}
	if response.Message.ResponseID != "resp_complete" || response.Message.Usage.TotalTokens != 2 {
		t.Fatalf("response metadata=%#v", response.Message)
	}

	simple, err := ai.CompleteSimple(context.Background(), model, ai.Context{Messages: []ai.Message{ai.NewUserMessage("short", nil)}}, ai.SimpleStreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(simple); got != "simple:1" {
		t.Fatalf("simple text=%q", got)
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := ai.GetAPIProvider("script-provider-test"); got != nil {
		t.Fatalf("provider should be unregistered after shutdown: %#v", got)
	}
}

func TestScriptProviderRequestNormalizesLegacySummaryMessages(t *testing.T) {
	req := scriptProviderRequestFromChatRequest(ai.ChatRequest{
		Model: ai.Model{Provider: "script", ID: "model", API: "script-provider-test"},
		Messages: []ai.Message{
			ai.CustomMessage{Role: "branchSummary", Summary: "branch work", TimestampMs: 1},
		},
	})
	if len(req.Messages) != 1 {
		t.Fatalf("messages=%#v", req.Messages)
	}
	if req.Messages[0].Role != "user" || len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Text != ai.BranchSummaryText("branch work") {
		t.Fatalf("message not normalized for script provider: %#v", req.Messages[0])
	}
}

// TestScriptExtensionProviderStreamsTokenDeltas verifies token-level streaming: a
// provider whose async generator yields multiple text chunks produces ordered
// start -> text_delta(>=2) -> done events with a monotonically growing Partial,
// and the final message equals the concatenated deltas (parity with collect).
func TestScriptExtensionProviderStreamsTokenDeltas(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "stream-provider-ext.mjs")
	source := `
export default function (pi) {
	pi.registerProvider({
		api: "script-stream-test",
		async *stream(req) {
			yield "Hello, ";
			yield "world";
			yield "!";
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := NewRunnerWithAPI(NewAPI())
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
		ai.UnregisterProviders(ext)
		ai.RegisterBuiltinProviders()
	})
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}

	model := ai.Model{Provider: "script", ID: "model", API: "script-stream-test"}
	llmContext := ai.Context{Messages: []ai.Message{ai.NewUserMessage("hi", nil)}}
	stream := ai.Stream(context.Background(), model, llmContext, ai.StreamOptions{})

	var types []string
	var deltas []string
	lastPartialLen := -1
	monotonic := true
	for ev := range stream.Events() {
		types = append(types, ev.Type)
		if ev.Type == "text_delta" {
			deltas = append(deltas, ev.Delta)
		}
		if plen := len(ai.MessageText(ev.Partial)); plen < lastPartialLen {
			monotonic = false
		} else {
			lastPartialLen = plen
		}
	}
	final := stream.Result()

	deltaCount := 0
	for _, ty := range types {
		if ty == "text_delta" {
			deltaCount++
		}
	}
	if deltaCount < 2 {
		t.Fatalf("want >=2 text_delta events (token streaming), got types=%v", types)
	}
	if len(types) == 0 || types[0] != "start" {
		t.Fatalf("first event should be start, got types=%v", types)
	}
	if types[len(types)-1] != "done" {
		t.Fatalf("last event should be done, got types=%v", types)
	}
	if !monotonic {
		t.Fatalf("Partial content should grow monotonically across events: types=%v", types)
	}
	if got := strings.Join(deltas, ""); got != "Hello, world!" {
		t.Fatalf("concatenated deltas=%q want %q", got, "Hello, world!")
	}
	if got := ai.MessageText(final); got != "Hello, world!" {
		t.Fatalf("final message text=%q want %q (final-message parity)", got, "Hello, world!")
	}
}

// TestScriptExtensionProviderStreamCancels verifies that cancelling the Go context
// mid-stream ends the stream promptly with an error/aborted terminal rather than
// hanging, and that the cancel request reaches the Node provider's abort signal.
func TestScriptExtensionProviderStreamCancels(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "stream-cancel-ext.mjs")
	// The generator yields one chunk then awaits a promise that only rejects when
	// the abort signal fires, so without cancellation it would hang forever.
	source := `
export default function (pi) {
	pi.registerProvider({
		api: "script-stream-cancel",
		async *stream(req, ctx, opts) {
			yield "first";
			await new Promise((resolve, reject) => {
				if (opts?.signal) opts.signal.addEventListener("abort", () => reject(new Error("aborted")));
			});
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := NewRunnerWithAPI(NewAPI())
	t.Cleanup(func() {
		_ = runtime.Shutdown(context.Background())
		ai.UnregisterProviders(ext)
		ai.RegisterBuiltinProviders()
	})
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}

	model := ai.Model{Provider: "script", ID: "model", API: "script-stream-cancel"}
	llmContext := ai.Context{Messages: []ai.Message{ai.NewUserMessage("hi", nil)}}
	ctx, cancel := context.WithCancel(context.Background())
	stream := ai.Stream(ctx, model, llmContext, ai.StreamOptions{})

	done := make(chan []string, 1)
	go func() {
		var types []string
		for ev := range stream.Events() {
			types = append(types, ev.Type)
			if ev.Type == "text_delta" {
				cancel() // cancel after the first streamed token
			}
		}
		done <- types
	}()

	select {
	case types := <-done:
		if len(types) == 0 || types[len(types)-1] != "error" {
			t.Fatalf("cancelled stream should terminate with an error event, got types=%v", types)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("cancelling the context did not end the provider stream (hang)")
	}
}

func TestScriptExtensionRegisterProviderModelCatalogBridge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "provider-models-ext.mjs")
	source := `
export default function (pi) {
	pi.registerProvider("script-catalog", {
		api: "openai-completions",
		baseUrl: "https://llm.test/v1",
		apiKey: "$SCRIPT_CATALOG_KEY",
		headers: { "X-Provider": "yes" },
		futureProviderField: { enabled: true },
		models: [{
			id: "coder",
			name: "Coder",
			input: ["text", "image"],
			reasoning: true,
			contextWindow: 123,
			maxTokens: 456,
			cost: { input: 1, output: 2 },
			headers: { "X-Model": "ok" },
			compat: { maxTokensField: "max_completion_tokens" },
		}],
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := NewRunnerWithAPI(NewAPI())
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	providers := runtime.RegisteredProviders()
	if len(providers) != 1 {
		t.Fatalf("registered providers=%#v", providers)
	}
	provider := providers[0]
	if provider.ProviderName != "script-catalog" || provider.API != "openai-completions" || provider.Provider != nil {
		t.Fatalf("provider metadata=%#v", provider)
	}
	var config ai.ProviderModelConfig
	if err := json.Unmarshal(provider.ModelConfig, &config); err != nil {
		t.Fatalf("model config json: %v", err)
	}
	if config.BaseURL != "https://llm.test/v1" || config.APIKey != "$SCRIPT_CATALOG_KEY" || len(config.Models) != 1 {
		t.Fatalf("model config=%#v", config)
	}
	if config.Headers["X-Provider"] != "yes" || config.Models[0].ID != "coder" {
		t.Fatalf("model config=%#v", config)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(provider.ModelConfig, &raw); err != nil {
		t.Fatalf("raw model config json: %v", err)
	}
	if _, ok := raw["futureProviderField"]; !ok {
		t.Fatalf("future provider field was dropped from model config: %s", provider.ModelConfig)
	}
}
