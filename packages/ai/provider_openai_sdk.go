package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	openairesponses "github.com/guanshan/pi-go/packages/ai/providers/openairesponses"
)

func doOpenAIChatCompletionSDK(ctx context.Context, req ChatRequest, key string, headers map[string]string, body map[string]any, bearerAuth bool) (ChatResponse, bool, error) {
	parsed, usedSDK, err := aiproviders.OpenAIChatCompletionSDK(ctx, openAISDKRequest(req, key, headers, body, bearerAuth))
	if err != nil {
		return ChatResponse{}, usedSDK, err
	}
	if !usedSDK {
		return ChatResponse{}, false, nil
	}
	return openAIChatResponse(parsed, req.Model), true, nil
}

func doOpenAIResponsesSDK(ctx context.Context, req ChatRequest, key string, headers map[string]string, body map[string]any) ([]byte, bool, error) {
	return aiproviders.OpenAIResponsesSDK(ctx, openAISDKRequest(req, key, headers, body, true))
}

func openAISDKRequest(req ChatRequest, key string, headers map[string]string, body map[string]any, bearerAuth bool) aiproviders.OpenAISDKRequest {
	return aiproviders.OpenAISDKRequest{
		API:            req.Model.API,
		BaseURL:        req.Model.BaseURL,
		Key:            key,
		Headers:        headers,
		Body:           body,
		BearerAuth:     bearerAuth,
		HTTPClient:     providerHTTPClient(req),
		RequestOptions: providerRequestOptions(req),
	}
}

func (r *ModelRegistry) openAIResponsesChatStream(ctx context.Context, req ChatRequest) *AssistantMessageEventStream {
	return providerStream(ctx, req.Model, 16, func(stream *AssistantMessageEventStream) (AssistantMessage, error) {
		return r.runOpenAIResponsesChatStream(ctx, req, stream)
	})
}

func (r *ModelRegistry) runOpenAIResponsesChatStream(ctx context.Context, req ChatRequest, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial := NewAssistantMessageForModel(req.Model, nil, Usage{}, "stop")
	if err := validateOpenAICodexResponsesTransport(req); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	key, err := r.APIKey(ctx, req.Model)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if key == "" {
		return openAIStreamError(partial, errors.New("No API key for provider: "+req.Model.Provider), stream)
	}
	body, err := openAIResponsesBody(req)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	url, headers, err := openAIResponsesRequest(req, key)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	sdkStream, usedSDK := aiproviders.OpenAIResponsesSDKStream(ctx, openAISDKRequest(req, key, headers, body, true))
	if !usedSDK {
		return r.runOpenAIResponsesHTTPStream(ctx, req, url, headers, body, partial, stream)
	}
	defer sdkStream.Close()

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	state := openairesponses.NewStreamState()
	tracker := newContentStreamTracker()
	sawEvent := false
	for sdkStream.Next() {
		if err := ctx.Err(); err != nil {
			openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
			return openAIStreamError(partial, err, stream)
		}
		event := sdkStream.Current()
		rawEvent := aiproviders.OpenAIResponseStreamEventMap(event)
		if len(rawEvent) > 0 {
			sawEvent = true
			for _, update := range state.Apply(rawEvent) {
				openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
				tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
		return openAIStreamError(partial, err, stream)
	}
	if err := sdkStream.Err(); err != nil {
		openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
		return openAIStreamError(partial, err, stream)
	}
	if !sawEvent {
		return r.runOpenAIResponsesHTTPStream(ctx, req, url, headers, body, partial, stream)
	}
	parsed := openAIResponsesParsedWithRequestDefaults(state.Parsed(), req)
	message, _ := openAIResponsesMessage(parsed, req.Model)
	applyOpenAICodexSSEFallbackDiagnostic(&message, req)
	if message.StopReason == "error" {
		if message.ErrorMessage == "" {
			message.ErrorMessage = "Provider returned an error stop reason"
		}
		return openAIStreamError(message, errors.New(message.ErrorMessage), stream)
	}
	tracker.Finish(stream, message)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
	return message, nil
}

func (r *ModelRegistry) runOpenAIResponsesHTTPStream(ctx context.Context, req ChatRequest, url string, headers map[string]string, body map[string]any, partial AssistantMessage, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	streamBody := cloneMap(body)
	streamBody["stream"] = true
	// MarshalJSON keeps < > & literal to match the TS upstream wire bytes (the
	// SDK path already disables HTML escaping; this is the manual HTTP fallback).
	rawBody, err := aiproviders.MarshalJSON(streamBody)
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return openAIStreamError(partial, err, stream)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "pi-go/"+Version)
	for k, v := range headers {
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
		parsed, err := aiproviders.ParseOpenAIResponses(raw)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		message, _ := openAIResponsesMessage(openAIResponsesParsedWithRequestDefaults(parsed, req), req.Model)
		applyOpenAICodexSSEFallbackDiagnostic(&message, req)
		pushAssistantMessage(stream, message)
		return message, nil
	}

	stream.Push(AssistantMessageEvent{Type: "start", Partial: partial})
	state := openairesponses.NewStreamState()
	tracker := newContentStreamTracker()
	sawEvent := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), streamScannerMaxLineBytes)
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if data == "" || data == "[DONE]" {
			return nil
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		sawEvent = true
		for _, update := range state.Apply(event) {
			openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
			tracker.PushDelta(stream, update.Type, update.ContentIndex, update.Delta, partial)
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
			return openAIStreamError(partial, err, stream)
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return openAIStreamError(partial, err, stream)
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := ctx.Err(); err != nil {
		openAIResponsesApplyPartial(&partial, openAIResponsesParsedWithRequestDefaults(state.Parsed(), req), req.Model)
		return openAIStreamError(partial, err, stream)
	}
	if err := scanner.Err(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if err := flush(); err != nil {
		return openAIStreamError(partial, err, stream)
	}
	if !sawEvent {
		response, err := r.openAIResponsesChat(ctx, req)
		if err != nil {
			return openAIStreamError(partial, err, stream)
		}
		pushAssistantMessage(stream, response.Message)
		return response.Message, nil
	}
	parsed := openAIResponsesParsedWithRequestDefaults(state.Parsed(), req)
	message, _ := openAIResponsesMessage(parsed, req.Model)
	applyOpenAICodexSSEFallbackDiagnostic(&message, req)
	if message.StopReason == "error" {
		if message.ErrorMessage == "" {
			message.ErrorMessage = "Provider returned an error stop reason"
		}
		return openAIStreamError(message, errors.New(message.ErrorMessage), stream)
	}
	tracker.Finish(stream, message)
	stream.Push(AssistantMessageEvent{Type: "done", Reason: doneReason(message.StopReason), Partial: message, Message: message})
	return message, nil
}

func openAIStreamError(partial AssistantMessage, err error, stream *AssistantMessageEventStream) (AssistantMessage, error) {
	partial.StopReason = stopReasonForError(err)
	if err != nil {
		partial.ErrorMessage = err.Error()
	}
	stream.Push(AssistantMessageEvent{Type: "error", Reason: errorReason(partial.StopReason), Partial: partial, Error: partial})
	return partial, err
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
