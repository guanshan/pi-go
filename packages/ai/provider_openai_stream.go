package ai

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	openaichat "github.com/guanshan/pi-go/packages/ai/providers/openaichat"
)

func (r *ModelRegistry) openAIChatStream(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runOpenAIChatStream(ctx, req, stream)
	})
}

func (r *ModelRegistry) runOpenAIChatStream(ctx context.Context, req ChatRequest, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	prepared, err := r.prepareOpenAIChatRequest(ctx, req)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	sdkStream, usedSDK := aiproviders.OpenAIChatCompletionSDKStream(ctx, openAISDKRequest(req, prepared.Key, prepared.Headers, prepared.Body, prepared.BearerAuth))
	if !usedSDK {
		return r.runOpenAIChatHTTPStream(ctx, req, prepared, partial, stream)
	}
	defer sdkStream.Close()

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	accumulator := openaichat.NewStreamAccumulator(req.Model.Provider)
	tracker := newContentStreamTracker()
	for sdkStream.Next() {
		if err := ctx.Err(); err != nil {
			return openAIStreamError(partial, err, stream)
		}
		chunk := sdkStream.Current()
		for _, update := range accumulator.Apply(chunk) {
			partial = openAIChatResponse(accumulator.Parsed(false), req.Model).Message
			tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
		}
	}
	if err := ctx.Err(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if err := sdkStream.Err(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if !accumulator.SawChunk() {
		response, err := r.openAIChat(ctx, req)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	message := openAIChatResponse(accumulator.Parsed(true), req.Model).Message
	if message.StopReason == "error" {
		if message.ErrorMessage == "" {
			message.ErrorMessage = "Provider returned an error stop reason"
		}
		return openAIStreamError(message, fmt.Errorf("%s", message.ErrorMessage), stream)
	}
	if !accumulator.SawFinishReason() {
		// Mirror openai-completions.ts: a stream that ends without a finish_reason
		// is truncated; surface an error so the retry whitelist ("ended without")
		// can re-issue the request instead of silently returning a partial "stop".
		return openAIStreamError(message, fmt.Errorf("Stream ended without finish_reason"), stream)
	}
	tracker.Finish(stream, message)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
	return message, nil
}

func (r *ModelRegistry) runOpenAIChatHTTPStream(ctx context.Context, req ChatRequest, prepared aiproviders.PreparedOpenAIChatRequest, partial AssistantMessage, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	body := aiproviders.CloneOpenAIChatBody(prepared.Body)
	body["stream"] = true
	if _, ok := body["stream_options"]; !ok {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	// MarshalJSON keeps < > & literal to match the TS upstream wire bytes (the
	// SDK path already disables HTML escaping; this is the manual HTTP fallback).
	rawBody, err := aiproviders.MarshalJSON(body)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, aiproviders.OpenAIChatURL(req.Model.BaseURL), bytes.NewReader(rawBody))
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "pi-go/"+Version)
	if prepared.BearerAuth {
		httpReq.Header.Set("Authorization", "Bearer "+prepared.Key)
	}
	for k, v := range prepared.Headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := providerHTTPClient(req).Do(httpReq)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	defer resp.Body.Close()
	if req.OnResponse != nil {
		if err := req.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: aiproviders.HeadersRecord(resp.Header)}, req.Model); err != nil {
			return openAIStreamError(partial, err, stream)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return openAIStreamError(partial, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))), stream)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream") {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		parsed, err := aiproviders.ParseOpenAIChatCompletionRawForProvider(raw, req.Model.Provider)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		message := openAIChatResponse(parsed, req.Model).Message
		pushAssistantMessage(stream, message)
		return message, nil
	}

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	accumulator := openaichat.NewStreamAccumulator(req.Model.Provider)
	tracker := newContentStreamTracker()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return openAIStreamError(partial, err, stream)
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		updates, err := accumulator.ApplyRaw([]byte(data))
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		for _, update := range updates {
			partial = openAIChatResponse(accumulator.Parsed(false), req.Model).Message
			tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
		}
	}
	if err := ctx.Err(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if err := scanner.Err(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if !accumulator.SawChunk() {
		response, err := r.openAIChat(ctx, req)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	message := openAIChatResponse(accumulator.Parsed(true), req.Model).Message
	if message.StopReason == "error" {
		if message.ErrorMessage == "" {
			message.ErrorMessage = "Provider returned an error stop reason"
		}
		return openAIStreamError(message, fmt.Errorf("%s", message.ErrorMessage), stream)
	}
	if !accumulator.SawFinishReason() {
		// Mirror openai-completions.ts: a stream that ends without a finish_reason
		// is truncated; surface an error so the retry whitelist ("ended without")
		// can re-issue the request instead of silently returning a partial "stop".
		return openAIStreamError(message, fmt.Errorf("Stream ended without finish_reason"), stream)
	}
	tracker.Finish(stream, message)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
	return message, nil
}

func shouldStreamOpenAIChat(model Model) bool {
	return model.API == "openai-completions"
}
