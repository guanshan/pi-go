package ai

import (
	"context"
	"strings"
)

type fauxAPIProvider struct{}

func registerFauxProvider() {
	registerBuiltinProvider(fauxAPIProvider{})
}

func (fauxAPIProvider) API() string { return "faux" }

func (fauxAPIProvider) complete(_ context.Context, _ *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return fauxChat(req), nil
}

func (p fauxAPIProvider) Complete(ctx context.Context, r *ModelRegistry, req ChatRequest) (ChatResponse, error) {
	return p.complete(ctx, r, req)
}

func (p fauxAPIProvider) Stream(ctx context.Context, _ *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return streamChatResponse(ctx, func(context.Context) (ChatResponse, error) {
		return p.complete(ctx, nil, req)
	})
}

func (p fauxAPIProvider) StreamSimple(ctx context.Context, r *ModelRegistry, req ChatRequest) *AssistantMessageEventStream {
	return p.Stream(ctx, r, req)
}

func fauxChat(req ChatRequest) ChatResponse {
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if MessageRole(req.Messages[i]) == "user" {
			last = MessageText(req.Messages[i])
			break
		}
	}
	text := "faux: " + last
	if strings.TrimSpace(last) == "" {
		text = "faux: ready"
	}
	msg := NewAssistantMessageForModel(req.Model, TextBlocks(text), Usage{Input: len(req.Messages), Output: len(strings.Fields(text)), TotalTokens: len(req.Messages) + len(strings.Fields(text))}, "stop")
	return ChatResponse{Message: msg}
}
