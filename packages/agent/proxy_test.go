package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestStreamProxyReconstructsMessage(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stream" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization") == "Bearer token"
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"start"}`,
			`{"type":"text_start","contentIndex":0}`,
			`{"type":"text_delta","contentIndex":0,"delta":"hel"}`,
			`{"type":"text_delta","contentIndex":0,"delta":"lo"}`,
			`{"type":"text_end","contentIndex":0,"contentSignature":"text-sig"}`,
			`{"type":"thinking_start","contentIndex":1}`,
			`{"type":"thinking_delta","contentIndex":1,"delta":"hmm"}`,
			`{"type":"thinking_end","contentIndex":1,"contentSignature":"thinking-sig"}`,
			`{"type":"toolcall_start","contentIndex":2,"id":"tc1","toolName":"read"}`,
			`{"type":"toolcall_delta","contentIndex":2,"delta":"{\"path\":"}`,
			`{"type":"toolcall_delta","contentIndex":2,"delta":"\"README.md\"}"}`,
			`{"type":"toolcall_end","contentIndex":2}`,
			`{"type":"done","reason":"toolUse","usage":{"input":1,"output":2,"totalTokens":3}}`,
		}
		for _, event := range events {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()

	stream := StreamProxy(ProxyStreamOptions{ProxyURL: server.URL, AuthToken: "token"})(context.Background(), ai.Model{Provider: "proxy", API: "proxy-api", ID: "m"}, ai.Context{
		Messages: []ai.Message{ai.NewUserMessage("hi", nil)},
	}, ai.StreamOptions{})
	var delta string
	for event := range stream.Events() {
		if event.Type == "text_delta" {
			delta += event.Delta
		}
	}
	message := stream.Result()
	if !sawAuth {
		t.Fatal("missing authorization header")
	}
	if delta != "hello" || message.StopReason != "toolUse" || message.Usage.TotalTokens != 3 {
		t.Fatalf("delta=%q message=%#v", delta, message)
	}
	blocks := ai.MessageBlocks(message)
	if len(blocks) != 3 || blocks[0].Text != "hello" || blocks[1].Thinking != "hmm" || blocks[2].Name != "read" {
		t.Fatalf("blocks=%#v", blocks)
	}
	if blocks[0].TextSignature != "text-sig" || blocks[1].ThinkingSignature != "thinking-sig" {
		t.Fatalf("signatures=%#v", blocks)
	}
	var args map[string]string
	if err := json.Unmarshal(blocks[2].Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != "README.md" {
		t.Fatalf("args=%#v raw=%s", args, blocks[2].Arguments)
	}
}

func TestStreamProxyHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	stream := StreamProxy(ProxyStreamOptions{ProxyURL: server.URL})(context.Background(), ai.Model{Provider: "proxy", ID: "m"}, ai.Context{}, ai.StreamOptions{})
	for range stream.Events() {
	}
	message := stream.Result()
	if message.StopReason != "error" {
		t.Fatalf("message=%#v", message)
	}
	if !strings.Contains(message.ErrorMessage, "proxy error") {
		t.Fatalf("errorMessage=%q", message.ErrorMessage)
	}
}

func TestProcessProxyEventInvalidToolCallDeltaUsesEmptyObject(t *testing.T) {
	partial := ai.NewAssistantMessage("api", "p", "m", nil, ai.Usage{}, "stop")
	if _, err := ProcessProxyEvent(ProxyAssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, ID: "tc1", ToolName: "read"}, &partial); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessProxyEvent(ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: "{"}, &partial); err != nil {
		t.Fatal(err)
	}
	blocks := ai.MessageBlocks(partial)
	if len(blocks) != 1 || string(blocks[0].Arguments) != "{}" {
		t.Fatalf("blocks=%#v", blocks)
	}
}

func TestProcessProxyEventErrorsOnMismatchedContent(t *testing.T) {
	partial := ai.NewAssistantMessage("api", "p", "m", nil, ai.Usage{}, "stop")
	if _, err := ProcessProxyEvent(ProxyAssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "x"}, &partial); err == nil {
		t.Fatal("expected mismatch error")
	}
}
