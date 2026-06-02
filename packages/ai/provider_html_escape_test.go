package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderRequestBodiesDoNotHTMLEscape locks the parity fix for the
// JSON HTML-escaping divergence: Go's encoding/json escapes < > & as
// < > &, but the TypeScript upstream's JSON.stringify emits them
// literally. Prompt text and tool results routinely contain these characters
// (HTML tags, &&, List<String>, a < b), and providers that hash the raw request
// body for prompt-cache lookups need the literal bytes. This test captures the
// raw outbound request body for each provider whose body our code serializes and
// asserts the HTML-significant characters are present literally and the escaped
// forms are absent.
func TestProviderRequestBodiesDoNotHTMLEscape(t *testing.T) {
	const needle = "a <b> && List<String> & c"

	respFor := func(api string) string {
		switch api {
		case "anthropic-messages":
			return `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`
		case "google-generative-ai":
			return `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`
		default: // mistral-conversations and openai-completions style
			return `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		}
	}

	cases := []struct {
		name     string
		provider string
		api      string
	}{
		{"anthropic", "anthropic", "anthropic-messages"},
		{"mistral", "mistral", "mistral-conversations"},
		{"google", "google", "google-generative-ai"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rawBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				rawBody = string(b)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(respFor(tc.api)))
			}))
			defer server.Close()

			registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
			registry.Auth.SetRuntime(tc.provider, "test-key")
			model := Model{Provider: tc.provider, ID: "x", API: tc.api, BaseURL: server.URL, Input: []string{"text"}}

			// Include the needle both in user text and in a tool result so both the
			// message-content and tool-result serialization paths are exercised.
			_, err := registry.StreamlessChat(context.Background(), ChatRequest{
				Model: model,
				Messages: []Message{
					NewUserMessage(needle, nil),
					NewToolResultMessage("call_1", "lookup", TextBlocks(needle), nil, false),
				},
			})
			if err != nil {
				t.Fatalf("chat failed: %v", err)
			}
			if rawBody == "" {
				t.Fatal("server captured no request body")
			}

			// The literal characters must survive on the wire.
			for _, frag := range []string{"<b>", "&&", "List<String>"} {
				if !strings.Contains(rawBody, frag) {
					t.Fatalf("request body missing literal %q\nbody=%s", frag, rawBody)
				}
			}
			// The Go HTML-escaped \uXXXX forms must NOT appear. Build the escape
			// sequences from explicit bytes to avoid any source-encoding ambiguity.
			escLT := string([]byte{'\\', 'u', '0', '0', '3', 'c'})
			escGT := string([]byte{'\\', 'u', '0', '0', '3', 'e'})
			escAmp := string([]byte{'\\', 'u', '0', '0', '2', '6'})
			for _, esc := range []string{escLT, escGT, escAmp} {
				if strings.Contains(rawBody, esc) {
					t.Fatalf("request body contains HTML-escaped %q (should be literal)\nbody=%s", esc, rawBody)
				}
			}
			// Body must still be valid JSON.
			var sink any
			if err := json.Unmarshal([]byte(rawBody), &sink); err != nil {
				t.Fatalf("request body is not valid JSON: %v\nbody=%s", err, rawBody)
			}
		})
	}
}

// TestMarshalJSONMatchesUnescape is a focused unit guard that the package-private
// no-HTML-escape marshal helper and the SDK-body unescaper agree byte-for-byte,
// including the corner case where a string literally contains the six-character
// text backslash-u-0-0-3-c (which must NOT be rewritten).
func TestMarshalJSONMatchesUnescape(t *testing.T) {
	literalU003c := string([]byte{'\\', 'u', '0', '0', '3', 'c'})
	values := []any{
		map[string]any{"text": "a <b> & c"},
		map[string]any{"s": literalU003c},
		map[string]any{"s": "x\\<y"},
		map[string]any{"a&b": "<v>", "emoji": "\U0001F600<tag>"},
	}
	for i, v := range values {
		noesc, err := marshalNoHTMLEscape(v)
		if err != nil {
			t.Fatalf("case %d marshalNoHTMLEscape: %v", i, err)
		}
		std, _ := json.Marshal(v)
		un := unescapeJSONHTML(std)
		if string(noesc) != string(un) {
			t.Fatalf("case %d mismatch: noesc=%s unescaped=%s", i, noesc, un)
		}
		// marshalNoHTMLEscape output must never contain the < HTML escape.
		escLT := string([]byte{'\\', 'u', '0', '0', '3', 'c'})
		if i != 1 && i != 2 && strings.Contains(string(noesc), escLT) {
			t.Fatalf("case %d unexpectedly still escaped: %s", i, noesc)
		}
	}
}
