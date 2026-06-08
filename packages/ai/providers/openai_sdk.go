package providers

import (
	"context"
	"encoding/json"
	"net/http"

	openai "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	openaiparam "github.com/openai/openai-go/v3/packages/param"
	openaissestream "github.com/openai/openai-go/v3/packages/ssestream"
	openairesponses "github.com/openai/openai-go/v3/responses"
	openaishared "github.com/openai/openai-go/v3/shared"
)

func NewOpenAIClient(key, baseURL string, headers map[string]string, bearerAuth bool, httpClient *http.Client, requestOptions ...RequestOptions) openai.Client {
	options := firstRequestOptions(requestOptions)
	opts := []openaioption.RequestOption{
		openaioption.WithRequestTimeout(RequestTimeout(options)),
	}
	if ShouldSetMaxRetries(options) {
		opts = append(opts, openaioption.WithMaxRetries(MaxRetries(options)))
	}
	if httpClient != nil {
		opts = append(opts, openaioption.WithHTTPClient(httpClient))
	}
	if baseURL != "" {
		opts = append(opts, openaioption.WithBaseURL(baseURL))
	}
	if bearerAuth {
		opts = append(opts, openaioption.WithAPIKey(key))
	} else {
		opts = append(opts, openaioption.WithAPIKey(""))
	}
	for k, v := range headers {
		opts = append(opts, openaioption.WithHeader(k, v))
	}
	if options.OnResponse != nil {
		opts = append(opts, openaioption.WithMiddleware(func(req *http.Request, next openaioption.MiddlewareNext) (*http.Response, error) {
			resp, err := next(req)
			if resp != nil {
				if responseErr := options.OnResponse(ProviderResponseFromHTTP(resp)); responseErr != nil {
					return resp, responseErr
				}
			}
			return resp, err
		}))
	}
	return openai.NewClient(opts...)
}

func CloneOpenAIChatBody(body map[string]any) map[string]any {
	out := make(map[string]any, len(body)+1)
	for key, value := range body {
		out[key] = value
	}
	return out
}

func OpenAIChatCompletionSDKParams(body map[string]any) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{}
	if model, _ := body["model"].(string); model != "" {
		params.Model = model
	}
	if messages, ok := body["messages"].([]map[string]any); ok {
		params.Messages = make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
		for _, message := range messages {
			params.Messages = append(params.Messages, openaiparam.Override[openai.ChatCompletionMessageParamUnion](message))
		}
	}
	if maxTokens, ok := intFromAny(body["max_tokens"]); ok {
		params.MaxTokens = openaiparam.NewOpt(int64(maxTokens))
	}
	if maxTokens, ok := intFromAny(body["max_completion_tokens"]); ok {
		params.MaxCompletionTokens = openaiparam.NewOpt(int64(maxTokens))
	}
	if temperature, ok := floatFromAny(body["temperature"]); ok {
		params.Temperature = openaiparam.NewOpt(temperature)
	}
	if tools, ok := body["tools"].([]map[string]any); ok {
		params.Tools = make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
		for _, tool := range tools {
			params.Tools = append(params.Tools, openaiparam.Override[openai.ChatCompletionToolUnionParam](tool))
		}
	}
	if toolChoice, ok := body["tool_choice"]; ok {
		if value, _ := toolChoice.(string); value != "" {
			params.ToolChoice.OfAuto = openaiparam.NewOpt(value)
		} else {
			params.ToolChoice = openaiparam.Override[openai.ChatCompletionToolChoiceOptionUnionParam](toolChoice)
		}
	}
	if cacheKey, _ := body["prompt_cache_key"].(string); cacheKey != "" {
		params.PromptCacheKey = openaiparam.NewOpt(cacheKey)
	}
	if retention, _ := body["prompt_cache_retention"].(string); retention != "" {
		params.PromptCacheRetention = openai.ChatCompletionNewParamsPromptCacheRetention(retention)
	}
	if effort, _ := body["reasoning_effort"].(string); effort != "" {
		params.ReasoningEffort = openaishared.ReasoningEffort(effort)
	}
	if extras := extraOpenAIChatCompletionFields(body); len(extras) > 0 {
		params.SetExtraFields(extras)
	}
	return params
}

func OpenAIResponseStreamEventMap(event openairesponses.ResponseStreamEventUnion) map[string]any {
	var out map[string]any
	if raw := event.RawJSON(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func OpenAIResponsesSDKParams(body map[string]any) openairesponses.ResponseNewParams {
	params := openairesponses.ResponseNewParams{}
	if model, _ := body["model"].(string); model != "" {
		params.Model = model
	}
	if input, ok := body["input"]; ok {
		params.Input = openaiparam.Override[openairesponses.ResponseNewParamsInputUnion](input)
	}
	if instructions, _ := body["instructions"].(string); instructions != "" {
		params.Instructions = openaiparam.NewOpt(instructions)
	}
	if store, ok := boolFromAny(body["store"]); ok {
		params.Store = openaiparam.NewOpt(store)
	}
	if maxTokens, ok := intFromAny(body["max_output_tokens"]); ok {
		params.MaxOutputTokens = openaiparam.NewOpt(int64(maxTokens))
	}
	if temperature, ok := floatFromAny(body["temperature"]); ok {
		params.Temperature = openaiparam.NewOpt(temperature)
	}
	if topP, ok := floatFromAny(body["top_p"]); ok {
		params.TopP = openaiparam.NewOpt(topP)
	}
	if maxToolCalls, ok := intFromAny(body["max_tool_calls"]); ok {
		params.MaxToolCalls = openaiparam.NewOpt(int64(maxToolCalls))
	}
	if parallel, ok := boolFromAny(body["parallel_tool_calls"]); ok {
		params.ParallelToolCalls = openaiparam.NewOpt(parallel)
	}
	if previous, _ := body["previous_response_id"].(string); previous != "" {
		params.PreviousResponseID = openaiparam.NewOpt(previous)
	}
	if cacheKey, _ := body["prompt_cache_key"].(string); cacheKey != "" {
		params.PromptCacheKey = openaiparam.NewOpt(cacheKey)
	}
	if retention, _ := body["prompt_cache_retention"].(string); retention != "" {
		params.PromptCacheRetention = openairesponses.ResponseNewParamsPromptCacheRetention(retention)
	}
	if include := responseIncludablesFromAny(body["include"]); len(include) > 0 {
		params.Include = include
	}
	if tools := responseToolParamsFromAny(body["tools"]); len(tools) > 0 {
		params.Tools = tools
	}
	if reasoning, ok := body["reasoning"]; ok {
		params.Reasoning = openaiparam.Override[openaishared.ReasoningParam](reasoning)
	}
	if text, ok := body["text"]; ok {
		params.Text = openaiparam.Override[openairesponses.ResponseTextConfigParam](text)
	}
	if toolChoice, ok := body["tool_choice"]; ok {
		params.ToolChoice = openaiparam.Override[openairesponses.ResponseNewParamsToolChoiceUnion](toolChoice)
	}
	if extras := extraOpenAIResponsesFields(body); len(extras) > 0 {
		params.SetExtraFields(extras)
	}
	return params
}

type OpenAISDKRequest struct {
	API            string
	BaseURL        string
	Key            string
	Headers        map[string]string
	Body           map[string]any
	BearerAuth     bool
	HTTPClient     *http.Client
	RequestOptions RequestOptions
	// SupportsUsageInStreaming gates the stream_options.include_usage injection
	// in the SDK chat stream. Mirrors openai-completions.ts which adds it only
	// when compat.supportsUsageInStreaming !== false.
	SupportsUsageInStreaming bool
}

func OpenAIChatCompletionSDK(ctx context.Context, req OpenAISDKRequest) (OpenAIChatParsed, bool, error) {
	resp, usedSDK, err := openAIChatCompletionSDKRaw(ctx, req)
	if !usedSDK || err != nil {
		return OpenAIChatParsed{}, usedSDK, err
	}
	parsed, err := OpenAIChatCompletionResponse(resp)
	return parsed, true, err
}

func OpenAIChatCompletionSDKStream(ctx context.Context, req OpenAISDKRequest) (*openaissestream.Stream[openai.ChatCompletionChunk], bool) {
	if !req.BearerAuth {
		return nil, false
	}
	baseURL, ok := SDKBaseURLForEndpoint(OpenAIChatURL(req.BaseURL), "/chat/completions")
	if !ok {
		return nil, false
	}
	if !IsOpenAIHostedBaseURL(baseURL) {
		return nil, false
	}
	client := NewOpenAIClient(req.Key, baseURL, SDKHeadersWithoutAuth(req.Headers), true, req.HTTPClient, req.RequestOptions)
	streamBody := CloneOpenAIChatBody(req.Body)
	if req.SupportsUsageInStreaming {
		if _, ok := streamBody["stream_options"]; !ok {
			streamBody["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	params := OpenAIChatCompletionSDKParams(streamBody)
	return client.Chat.Completions.NewStreaming(ctx, params), true
}

func OpenAIResponsesSDK(ctx context.Context, req OpenAISDKRequest) ([]byte, bool, error) {
	if req.API != "openai-responses" {
		return nil, false, nil
	}
	baseURL, ok := SDKBaseURLForEndpoint(OpenAIResponsesURL(req.BaseURL), "/responses")
	if !ok {
		return nil, false, nil
	}
	client := NewOpenAIClient(req.Key, baseURL, SDKHeadersWithoutAuth(req.Headers), true, req.HTTPClient, req.RequestOptions)
	params := OpenAIResponsesSDKParams(req.Body)
	resp, err := client.Responses.New(ctx, params)
	if err != nil {
		return nil, true, err
	}
	return []byte(resp.RawJSON()), true, nil
}

func OpenAIResponsesSDKStream(ctx context.Context, req OpenAISDKRequest) (*openaissestream.Stream[openairesponses.ResponseStreamEventUnion], bool) {
	if req.API != "openai-responses" {
		return nil, false
	}
	baseURL, ok := SDKBaseURLForEndpoint(OpenAIResponsesURL(req.BaseURL), "/responses")
	if !ok {
		return nil, false
	}
	client := NewOpenAIClient(req.Key, baseURL, SDKHeadersWithoutAuth(req.Headers), true, req.HTTPClient, req.RequestOptions)
	params := OpenAIResponsesSDKParams(req.Body)
	return client.Responses.NewStreaming(ctx, params), true
}

func openAIChatCompletionSDKRaw(ctx context.Context, req OpenAISDKRequest) (*openai.ChatCompletion, bool, error) {
	if !req.BearerAuth {
		return nil, false, nil
	}
	baseURL, ok := SDKBaseURLForEndpoint(OpenAIChatURL(req.BaseURL), "/chat/completions")
	if !ok {
		return nil, false, nil
	}
	if !IsOpenAIHostedBaseURL(baseURL) {
		return nil, false, nil
	}
	client := NewOpenAIClient(req.Key, baseURL, SDKHeadersWithoutAuth(req.Headers), true, req.HTTPClient, req.RequestOptions)
	params := OpenAIChatCompletionSDKParams(req.Body)
	resp, err := client.Chat.Completions.New(ctx, params)
	return resp, true, err
}

func extraOpenAIChatCompletionFields(body map[string]any) map[string]any {
	extras := map[string]any{}
	for key, value := range body {
		switch key {
		case "model", "messages", "max_tokens", "max_completion_tokens", "temperature", "tools", "tool_choice", "prompt_cache_key", "prompt_cache_retention", "reasoning_effort":
			continue
		default:
			extras[key] = value
		}
	}
	return extras
}

func intFromAny(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func floatFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func boolFromAny(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	default:
		return false, false
	}
}

func stringsFromAny(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func responseIncludablesFromAny(value any) []openairesponses.ResponseIncludable {
	values := stringsFromAny(value)
	if len(values) == 0 {
		return nil
	}
	out := make([]openairesponses.ResponseIncludable, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, openairesponses.ResponseIncludable(value))
		}
	}
	return out
}

func responseToolParamsFromAny(value any) []openairesponses.ToolUnionParam {
	switch tools := value.(type) {
	case []map[string]any:
		out := make([]openairesponses.ToolUnionParam, 0, len(tools))
		for _, tool := range tools {
			out = append(out, openaiparam.Override[openairesponses.ToolUnionParam](tool))
		}
		return out
	case []any:
		out := make([]openairesponses.ToolUnionParam, 0, len(tools))
		for _, tool := range tools {
			out = append(out, openaiparam.Override[openairesponses.ToolUnionParam](tool))
		}
		return out
	default:
		return nil
	}
}

func extraOpenAIResponsesFields(body map[string]any) map[string]any {
	extras := map[string]any{}
	for key, value := range body {
		switch key {
		case "model", "input", "instructions", "store", "max_output_tokens", "temperature", "top_p", "max_tool_calls", "parallel_tool_calls", "previous_response_id", "prompt_cache_key", "prompt_cache_retention", "include", "tools", "reasoning", "text", "tool_choice":
			continue
		default:
			extras[key] = value
		}
	}
	return extras
}
