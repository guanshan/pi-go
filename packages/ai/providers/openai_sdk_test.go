package providers

import (
	"encoding/json"
	"testing"
)

func TestOpenAIChatCompletionSDKParamsPreserveCompatFields(t *testing.T) {
	params := OpenAIChatCompletionSDKParams(map[string]any{
		"model":                  "gpt-test",
		"messages":               []map[string]any{{"role": "developer", "content": "rules"}},
		"max_completion_tokens":  float64(123),
		"reasoning_effort":       "high",
		"prompt_cache_key":       "session-key",
		"prompt_cache_retention": "24h",
		"provider":               map[string]any{"only": []string{"anthropic"}},
		"stream_options":         map[string]any{"include_usage": true},
	})
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["max_completion_tokens"] != float64(123) {
		t.Fatalf("max_completion_tokens=%#v body=%s", body["max_completion_tokens"], raw)
	}
	if body["reasoning_effort"] != "high" || body["prompt_cache_key"] != "session-key" || body["prompt_cache_retention"] != "24h" {
		t.Fatalf("compat fields body=%s", raw)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("max_tokens should be omitted body=%s", raw)
	}
	if _, ok := body["provider"].(map[string]any); !ok {
		t.Fatalf("provider extra field missing body=%s", raw)
	}
	if streamOptions, ok := body["stream_options"].(map[string]any); !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options=%#v body=%s", body["stream_options"], raw)
	}
}

func TestOpenAIResponsesSDKParamsPreserveCompatFields(t *testing.T) {
	params := OpenAIResponsesSDKParams(map[string]any{
		"model":                  "gpt-test",
		"input":                  []map[string]any{{"role": "user", "content": "hi"}},
		"store":                  false,
		"max_output_tokens":      float64(77),
		"parallel_tool_calls":    true,
		"prompt_cache_key":       "session-key",
		"prompt_cache_retention": "24h",
		"include":                []string{"reasoning.encrypted_content"},
		"reasoning":              map[string]any{"effort": "medium", "summary": "auto"},
		"text":                   map[string]any{"verbosity": "low"},
		"tool_choice":            "auto",
		"service_tier":           "flex",
	})
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["store"] != false || body["max_output_tokens"] != float64(77) || body["parallel_tool_calls"] != true {
		t.Fatalf("core fields body=%s", raw)
	}
	if body["prompt_cache_key"] != "session-key" || body["prompt_cache_retention"] != "24h" {
		t.Fatalf("cache fields body=%s", raw)
	}
	if _, ok := body["reasoning"].(map[string]any); !ok {
		t.Fatalf("reasoning missing body=%s", raw)
	}
	if _, ok := body["text"].(map[string]any); !ok || body["tool_choice"] != "auto" {
		t.Fatalf("text/tool_choice body=%s", raw)
	}
	if body["service_tier"] != "flex" {
		t.Fatalf("extra field service_tier=%#v body=%s", body["service_tier"], raw)
	}
}
