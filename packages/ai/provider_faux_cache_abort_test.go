package ai

import (
	"context"
	"testing"
)

func TestFauxSimulatesPerSessionCache(t *testing.T) {
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	reg := RegisterFauxProvider(NewFauxText("first"), NewFauxText("second"))
	t.Cleanup(reg.Unregister)

	ctx := Context{SystemPrompt: "system", Messages: []Message{NewUserMessage("same prompt", nil)}}
	options := StreamOptions{SessionID: "session-cache", CacheRetention: "long"}
	first, err := registry.Complete(context.Background(), reg.Model, ctx, options)
	if err != nil {
		t.Fatalf("first complete: %v", err)
	}
	if first.Usage.CacheRead != 0 || first.Usage.Input <= 0 {
		t.Fatalf("first usage=%#v, want uncached input", first.Usage)
	}
	second, err := registry.Complete(context.Background(), reg.Model, ctx, options)
	if err != nil {
		t.Fatalf("second complete: %v", err)
	}
	if second.Usage.CacheRead <= 0 || second.Usage.Input != 0 {
		t.Fatalf("second usage=%#v, want prompt moved to cacheRead", second.Usage)
	}
	if second.Usage.TotalTokens != second.Usage.Output+second.Usage.CacheRead {
		t.Fatalf("second totalTokens=%d usage=%#v", second.Usage.TotalTokens, second.Usage)
	}
}

func TestFauxCacheRetentionNoneDisablesSessionCache(t *testing.T) {
	registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
	reg := RegisterFauxProvider(NewFauxText("first"), NewFauxText("second"))
	t.Cleanup(reg.Unregister)

	ctx := Context{Messages: []Message{NewUserMessage("same prompt", nil)}}
	options := StreamOptions{SessionID: "session-cache-none", CacheRetention: "none"}
	for i := 0; i < 2; i++ {
		msg, err := registry.Complete(context.Background(), reg.Model, ctx, options)
		if err != nil {
			t.Fatalf("complete %d: %v", i, err)
		}
		if msg.Usage.CacheRead != 0 || msg.Usage.Input <= 0 {
			t.Fatalf("complete %d usage=%#v, cache should be disabled", i, msg.Usage)
		}
	}
}

func TestFauxAbortMidStreamPaced(t *testing.T) {
	tests := []struct {
		name      string
		response  FauxResponse
		deltaType string
	}{
		{
			name:      "text",
			response:  FauxResponse{Content: []ContentBlock{FauxText("abcdef")}, TokensPerSecond: 1000, TokenSize: 1},
			deltaType: "text_delta",
		},
		{
			name:      "thinking",
			response:  FauxResponse{Content: []ContentBlock{FauxThinking("abcdef")}, TokensPerSecond: 1000, TokenSize: 1},
			deltaType: "thinking_delta",
		},
		{
			name:      "toolcall",
			response:  FauxResponse{Content: []ContentBlock{FauxToolCall("call-1", "echo", map[string]any{"text": "abcdef"})}, TokensPerSecond: 1000, TokenSize: 1},
			deltaType: "toolcall_delta",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir(), NewAuthStorage(t.TempDir()))
			reg := RegisterFauxProvider(tc.response)
			t.Cleanup(reg.Unregister)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream := registry.Stream(ctx, reg.Model, Context{Messages: []Message{NewUserMessage("go", nil)}}, StreamOptions{})
			sawDelta := false
			for event := range stream.Events() {
				if event.Type == tc.deltaType {
					sawDelta = true
					cancel()
				}
			}
			result := stream.Result()
			if !sawDelta {
				t.Fatalf("did not see %s before abort", tc.deltaType)
			}
			if result.StopReason != "aborted" {
				t.Fatalf("stopReason=%q result=%#v", result.StopReason, result)
			}
		})
	}
}
