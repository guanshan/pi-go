package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestGoogleVertexChatPayloadAndParse(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "project-1")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	var captured map[string]any
	var path string
	var query string
	var apiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		apiKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"parts":[
				{"text":"thought","thought":true,"thoughtSignature":"dGhvdWdodA=="},
				{"text":"vertex","thoughtSignature":"dGV4dA=="},
				{"functionCall":{"id":"call-v","name":"lookup","args":{"q":"x"}}}
			]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":9,"cachedContentTokenCount":2,"candidatesTokenCount":4,"thoughtsTokenCount":3,"totalTokenCount":16}
		}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google-vertex", "vertex-key")
	temp := 0.1
	response, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "google-vertex",
			ID:        "gemini-3-pro",
			API:       "google-vertex",
			BaseURL:   server.URL,
			Input:     []string{"text", "image"},
			Reasoning: true,
			MaxOutput: 123,
		},
		SystemPrompt:  "system",
		Messages:      []Message{NewUserMessage("hello", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}})},
		Tools:         ToolSet{"read": cacheTestToolDef("read")},
		ThinkingLevel: ThinkingLow,
		Temperature:   &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/v1/publishers/google/models/gemini-3-pro:generateContent") {
		t.Fatalf("path=%q", path)
	}
	if query != "" {
		t.Fatalf("query=%q", query)
	}
	if apiKey != "vertex-key" {
		t.Fatalf("x-goog-api-key=%q", apiKey)
	}
	config := captured["generationConfig"].(map[string]any)
	// TS google-vertex.ts:438-440 sets maxOutputTokens ONLY when the caller
	// passed options.maxTokens; this request did not, so the field is omitted
	// (no fallback to the model's MaxOutput, no 1024 floor).
	if _, ok := config["maxOutputTokens"]; ok {
		t.Fatalf("maxOutputTokens should be omitted when MaxTokens unset: %#v", config)
	}
	if config["temperature"] != 0.1 {
		t.Fatalf("generationConfig=%#v", config)
	}
	thinkingConfig := config["thinkingConfig"].(map[string]any)
	if thinkingConfig["includeThoughts"] != true || thinkingConfig["thinkingLevel"] != "LOW" {
		t.Fatalf("thinkingConfig=%#v", thinkingConfig)
	}
	contents := captured["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	if _, ok := parts[1].(map[string]any)["inlineData"]; !ok {
		t.Fatalf("missing inlineData: %#v", parts)
	}
	tools := captured["tools"].([]any)
	declarations := tools[0].(map[string]any)["functionDeclarations"].([]any)
	if declarations[0].(map[string]any)["name"] != "read" {
		t.Fatalf("function declarations=%#v", declarations)
	}

	blocks := MessageBlocks(response.Message)
	if len(blocks) != 3 || blocks[0].Thinking != "thought" || blocks[1].Text != "vertex" || blocks[2].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if blocks[0].Signature != "dGhvdWdodA==" || blocks[1].TextSignature != "dGV4dA==" {
		t.Fatalf("signatures=%#v", blocks)
	}
	if response.Message.StopReason != "toolUse" {
		t.Fatalf("stopReason=%q", response.Message.StopReason)
	}
	if response.Message.Usage.Input != 7 || response.Message.Usage.Output != 7 || response.Message.Usage.CacheRead != 2 {
		t.Fatalf("usage=%#v", response.Message.Usage)
	}
}

func TestGoogleVertexHasAuthWithADC(t *testing.T) {
	adc := filepath.Join(t.TempDir(), "application_default_credentials.json")
	if err := os.WriteFile(adc, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "project-1")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	if !registry.HasAuth(Model{Provider: "google-vertex", API: "google-vertex"}) {
		t.Fatal("expected Vertex ADC plus project/location to satisfy auth")
	}
}

func TestGoogleGemini3DisabledThinkingToolChoiceAndToolResultImages(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google", "test-key")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "google",
			ID:        "gemini-3-pro",
			API:       "google-generative-ai",
			BaseURL:   server.URL,
			Input:     []string{"text", "image"},
			Reasoning: true,
		},
		Messages: []Message{
			NewToolResultMessage("call_1", "vision", []ContentBlock{
				{Type: "text", Text: "result"},
				{Type: "image", MimeType: "image/png", Data: "toolimg"},
			}, nil, false),
		},
		Tools:      ToolSet{"vision": cacheTestToolDef("vision")},
		ToolChoice: "any",
	})
	if err != nil {
		t.Fatal(err)
	}
	config := captured["generationConfig"].(map[string]any)
	thinking := config["thinkingConfig"].(map[string]any)
	if thinking["thinkingLevel"] != "LOW" {
		t.Fatalf("thinkingConfig=%#v", thinking)
	}
	if _, ok := thinking["includeThoughts"]; ok {
		t.Fatalf("disabled thinking should not include thoughts: %#v", thinking)
	}
	toolConfig := captured["toolConfig"].(map[string]any)
	functionCalling := toolConfig["functionCallingConfig"].(map[string]any)
	if functionCalling["mode"] != "ANY" {
		t.Fatalf("toolConfig=%#v", toolConfig)
	}
	parts := captured["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	functionResponse := parts[0].(map[string]any)["functionResponse"].(map[string]any)
	if functionResponse["name"] != "vision" {
		t.Fatalf("functionResponse=%#v", functionResponse)
	}
	if _, ok := functionResponse["id"]; ok {
		t.Fatalf("Gemini function responses should omit tool call id: %#v", functionResponse)
	}
	response := functionResponse["response"].(map[string]any)
	if response["output"] != "result" {
		t.Fatalf("function response payload=%#v", response)
	}
	imageParts := functionResponse["parts"].([]any)
	if _, ok := imageParts[0].(map[string]any)["inlineData"]; !ok {
		t.Fatalf("function response image parts=%#v", imageParts)
	}
}

func TestGoogleThoughtSignaturesAndToolCallIDRules(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google", "test-key")
	assistant := NewAssistantMessage("google-generative-ai", "google", "claude-test", []ContentBlock{
		{Type: "text", Text: "visible", TextSignature: "not base64"},
		{Type: "thinking", Thinking: "private", Signature: "c2ln"},
		{Type: "toolCall", ID: "call with symbols!!", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ThoughtSignature: "dGhvdWdodA=="},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{Provider: "google", ID: "claude-test", API: "google-generative-ai", BaseURL: server.URL, Input: []string{"text"}, Reasoning: true},
		Messages: []Message{
			assistant,
			NewToolResultMessage("call with symbols!!", "lookup", TextBlocks("ok"), nil, false),
		},
		Tools: ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	if err != nil {
		t.Fatal(err)
	}
	contents := captured["contents"].([]any)
	modelParts := contents[0].(map[string]any)["parts"].([]any)
	if _, ok := modelParts[0].(map[string]any)["thoughtSignature"]; ok {
		t.Fatalf("invalid text signature should be omitted: %#v", modelParts[0])
	}
	if _, ok := modelParts[1].(map[string]any)["thoughtSignature"]; !ok {
		t.Fatalf("valid thinking signature missing: %#v", modelParts[1])
	}
	functionCall := modelParts[2].(map[string]any)["functionCall"].(map[string]any)
	if functionCall["id"] != "call_with_symbols__" {
		t.Fatalf("normalized function call id=%#v", functionCall["id"])
	}
	if _, ok := modelParts[2].(map[string]any)["thoughtSignature"]; !ok {
		t.Fatalf("tool call thought signature missing: %#v", modelParts[2])
	}
	functionResponse := contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if functionResponse["id"] != "call_with_symbols__" {
		t.Fatalf("normalized function response id=%#v", functionResponse["id"])
	}
}

func TestGoogleGemini3UnsignedToolCallsDoNotAddValidatorBypass(t *testing.T) {
	model := Model{Provider: "google", ID: "gemini-3-pro-preview", API: "google-generative-ai", BaseURL: "https://example.com", Input: []string{"text"}, Reasoning: true}
	assistant := NewAssistantMessage("google-generative-ai", "google", "other-model", []ContentBlock{
		{Type: "toolCall", ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command":"echo hi"}`)},
		{Type: "toolCall", ID: "call_2", Name: "bash", Arguments: json.RawMessage(`{"command":"ls -la"}`)},
	}, Usage{}, "toolUse")
	payload, err := googleGeneratePayload(ChatRequest{
		Model:    model,
		Messages: []Message{NewUserMessage("Hi", nil), assistant},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	modelTurn := googleModelTurn(t, payload)
	var functionCalls int
	for _, part := range modelTurn.Parts {
		if part.FunctionCall != nil {
			functionCalls++
			if len(part.ThoughtSignature) != 0 {
				t.Fatalf("unexpected thought signature on function call: %#v", part)
			}
		}
		if strings.Contains(part.Text, "skip_thought_signature_validator") || strings.Contains(part.Text, "Historical context") {
			t.Fatalf("unexpected validator bypass text: %#v", part)
		}
	}
	if functionCalls != 2 {
		t.Fatalf("function call count=%d", functionCalls)
	}
}

func TestGoogleGemini3PreservesSameModelToolThoughtSignature(t *testing.T) {
	model := Model{Provider: "google", ID: "gemini-3-pro-preview", API: "google-generative-ai", BaseURL: "https://example.com", Input: []string{"text"}, Reasoning: true}
	validSignature := "AAAAAAAAAAAAAAAAAAAAAA=="
	assistant := NewAssistantMessage(model.API, model.Provider, model.ID, []ContentBlock{
		{Type: "toolCall", ID: "call_1", Name: "bash", Arguments: json.RawMessage(`{"command":"echo hi"}`), ThoughtSignature: validSignature},
		{Type: "toolCall", ID: "call_2", Name: "bash", Arguments: json.RawMessage(`{"command":"ls -la"}`)},
	}, Usage{}, "toolUse")
	payload, err := googleGeneratePayload(ChatRequest{
		Model:    model,
		Messages: []Message{NewUserMessage("Hi", nil), assistant},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	modelTurn := googleModelTurn(t, payload)
	var signatures []string
	for _, part := range modelTurn.Parts {
		if part.FunctionCall == nil {
			continue
		}
		if len(part.ThoughtSignature) == 0 {
			signatures = append(signatures, "")
			continue
		}
		signatures = append(signatures, base64.StdEncoding.EncodeToString(part.ThoughtSignature))
	}
	if len(signatures) != 2 || signatures[0] != validSignature || signatures[1] != "" {
		t.Fatalf("signatures=%#v", signatures)
	}
}

func googleModelTurn(t *testing.T, payload GoogleGeneratePayload) *genai.Content {
	t.Helper()
	for _, content := range payload.Contents {
		if content.Role == "model" {
			return content
		}
	}
	t.Fatal("missing model turn")
	return nil
}

func TestGoogleChatStreamEmitsDeltas(t *testing.T) {
	var captured map[string]any
	var path string
	var query string
	var apiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		apiKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data:{"candidates":[{"content":{"role":"model","parts":[{"text":"think","thought":true,"thoughtSignature":"dGhpbms="}]}}]}` + "\n\n" +
				`data:{"candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}` + "\n\n" +
				`data:{"candidates":[{"content":{"role":"model","parts":[{"text":"lo","thoughtSignature":"dGV4dA=="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"cachedContentTokenCount":1,"candidatesTokenCount":2,"thoughtsTokenCount":3,"totalTokenCount":10}}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google", "test-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "google",
			ID:        "gemini-test",
			API:       "google-generative-ai",
			BaseURL:   server.URL,
			Input:     []string{"text"},
			Reasoning: true,
		},
		SystemPrompt:  "system",
		Messages:      []Message{NewUserMessage("hi", nil)},
		ThinkingLevel: ThinkingLow,
	})
	var textDeltas []string
	var thinkingDeltas []string
	for event := range stream.Events() {
		switch event.Type {
		case "text_delta":
			textDeltas = append(textDeltas, event.Delta)
		case "thinking_delta":
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		}
	}
	message := stream.Result()
	if !strings.HasSuffix(path, "/models/gemini-test:streamGenerateContent") {
		t.Fatalf("path=%q", path)
	}
	if query != "alt=sse" {
		t.Fatalf("query=%q", query)
	}
	if apiKey != "test-key" {
		t.Fatalf("x-goog-api-key=%q", apiKey)
	}
	if captured["systemInstruction"] == nil {
		t.Fatalf("missing systemInstruction: %#v", captured)
	}
	if got := strings.Join(textDeltas, ""); got != "hello" {
		t.Fatalf("text deltas=%q", got)
	}
	if got := strings.Join(thinkingDeltas, ""); got != "think" {
		t.Fatalf("thinking deltas=%q", got)
	}
	blocks := MessageBlocks(message)
	if len(blocks) != 2 || blocks[0].Thinking != "think" || blocks[1].Text != "hello" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if blocks[0].Signature != "dGhpbms=" || blocks[1].TextSignature != "dGV4dA==" {
		t.Fatalf("signatures=%#v", blocks)
	}
	if message.Usage.Input != 4 || message.Usage.Output != 5 || message.Usage.CacheRead != 1 || message.Usage.TotalTokens != 10 {
		t.Fatalf("usage=%#v", message.Usage)
	}
}

func TestGoogleVertexChatStreamAggregatesToolCalls(t *testing.T) {
	var path string
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data:{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-v","name":"lookup","args":{"q":"go"}}}]},"finishReason":"STOP"}]}` + "\n\n",
		))
	}))
	defer server.Close()

	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	registry.Auth.SetRuntime("google-vertex", "vertex-key")
	stream := registry.StreamChat(context.Background(), ChatRequest{
		Model: Model{
			Provider: "google-vertex",
			ID:       "gemini-3-pro",
			API:      "google-vertex",
			BaseURL:  server.URL,
			Input:    []string{"text"},
		},
		Messages: []Message{NewUserMessage("hi", nil)},
		Tools:    ToolSet{"lookup": cacheTestToolDef("lookup")},
	})
	var toolDelta bool
	for event := range stream.Events() {
		if event.Type == "toolcall_delta" {
			toolDelta = true
		}
	}
	message := stream.Result()
	if !strings.HasSuffix(path, "/v1/publishers/google/models/gemini-3-pro:streamGenerateContent") {
		t.Fatalf("path=%q", path)
	}
	if query != "alt=sse" {
		t.Fatalf("query=%q", query)
	}
	blocks := MessageBlocks(message)
	if len(blocks) != 1 || blocks[0].Type != "toolCall" || blocks[0].ID != "call-v" || blocks[0].Name != "lookup" {
		t.Fatalf("blocks=%#v", blocks)
	}
	var args map[string]string
	if err := json.Unmarshal(blocks[0].Arguments, &args); err != nil || args["q"] != "go" {
		t.Fatalf("arguments=%s", blocks[0].Arguments)
	}
	if message.StopReason != "toolUse" || !toolDelta {
		t.Fatalf("stopReason=%q toolDelta=%v", message.StopReason, toolDelta)
	}
}
