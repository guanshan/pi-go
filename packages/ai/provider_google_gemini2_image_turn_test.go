package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGoogleGemini2xSeparateSyntheticImageTurn ports the Gemini 2.x branch of
// ../pi/packages/ai/test/google-shared-image-tool-result-routing.test.ts
// (~lines 79-88). Unlike Gemini 3 (which nests image parts inside the
// functionResponse — covered by TestGoogleGemini3DisabledThinkingToolChoiceAndToolResultImages),
// Gemini 2.x emits a SEPARATE synthetic "Tool result image:" user turn carrying
// inlineData, because google.go:googleSupportsMultimodalFunctionResponse returns
// false for non-gemini-3 models.
//
// Conversation: user, assistant(3 toolCalls), toolResult(text), toolResult(image),
// toolResult(text). Expected contents (5 turns):
//
//	[0] user        (original prompt)
//	[1] model       (3 functionCalls)
//	[2] user        (functionResponse for call_a + functionResponse for call_img)
//	[3] user        ({text:"Tool result image:"} + inlineData)
//	[4] user        (functionResponse for call_b)
func TestGoogleGemini2xSeparateSyntheticImageTurn(t *testing.T) {
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
	assistant := NewAssistantMessage("google-generative-ai", "google", "gemini-2.5-flash", []ContentBlock{
		{Type: "toolCall", ID: "call_a", Name: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		{Type: "toolCall", ID: "call_img", Name: "read", Arguments: json.RawMessage(`{"path":"image.png"}`)},
		{Type: "toolCall", ID: "call_b", Name: "read", Arguments: json.RawMessage(`{"path":"b.txt"}`)},
	}, Usage{}, "toolUse")
	_, err := registry.StreamlessChat(context.Background(), ChatRequest{
		Model: Model{
			Provider:  "google",
			ID:        "gemini-2.5-flash",
			API:       "google-generative-ai",
			BaseURL:   server.URL,
			Input:     []string{"text", "image"},
			Reasoning: true,
		},
		Messages: []Message{
			NewUserMessage("read the files", nil),
			assistant,
			NewToolResultMessage("call_a", "read", TextBlocks("alpha text"), nil, false),
			NewToolResultMessage("call_img", "read", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}, nil, false),
			NewToolResultMessage("call_b", "read", TextBlocks("beta text"), nil, false),
		},
		Tools: ToolSet{"read": cacheTestToolDef("read")},
	})
	if err != nil {
		t.Fatal(err)
	}

	contents, ok := captured["contents"].([]any)
	if !ok {
		t.Fatalf("contents not an array: %#v", captured["contents"])
	}
	if len(contents) != 5 {
		t.Fatalf("contents length=%d, want 5: %#v", len(contents), contents)
	}

	// contents[2]: every part is a functionResponse (text result + image result
	// both routed as functionResponses on the same user turn).
	parts2 := contentParts(t, contents[2])
	if len(parts2) == 0 {
		t.Fatalf("contents[2] has no parts: %#v", contents[2])
	}
	for _, part := range parts2 {
		if _, has := part["functionResponse"]; !has {
			t.Fatalf("contents[2] part is not a functionResponse: %#v", part)
		}
	}

	// contents[3]: the SEPARATE synthetic image turn.
	parts3 := contentParts(t, contents[3])
	if len(parts3) < 2 {
		t.Fatalf("contents[3] expected >=2 parts (text + image): %#v", parts3)
	}
	if parts3[0]["text"] != "Tool result image:" {
		t.Fatalf("contents[3].parts[0].text=%#v, want \"Tool result image:\"", parts3[0]["text"])
	}
	if _, has := parts3[1]["inlineData"]; !has {
		t.Fatalf("contents[3].parts[1] missing inlineData: %#v", parts3[1])
	}

	// contents[4]: the final text tool result, routed as a functionResponse.
	parts4 := contentParts(t, contents[4])
	if len(parts4) == 0 {
		t.Fatalf("contents[4] has no parts: %#v", contents[4])
	}
	if _, has := parts4[0]["functionResponse"]; !has {
		t.Fatalf("contents[4].parts[0] not a functionResponse: %#v", parts4[0])
	}
}

func contentParts(t *testing.T, content any) []map[string]any {
	t.Helper()
	m, ok := content.(map[string]any)
	if !ok {
		t.Fatalf("content not an object: %#v", content)
	}
	raw, ok := m["parts"].([]any)
	if !ok {
		t.Fatalf("content.parts not an array: %#v", m["parts"])
	}
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		part, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("part not an object: %#v", p)
		}
		out[i] = part
	}
	return out
}
