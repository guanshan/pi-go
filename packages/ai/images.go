package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	imageproviders "github.com/guanshan/pi-go/packages/ai/providers/images"
)

type ImagesModel struct {
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	API      string            `json:"api"`
	BaseURL  string            `json:"baseUrl,omitempty"`
	EnvKey   string            `json:"envKey,omitempty"`
	Input    []string          `json:"input,omitempty"`
	Output   []string          `json:"output,omitempty"`
	Cost     Cost              `json:"cost,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// imagesModelJSON mirrors the upstream TS ImagesModel wire shape, where the
// catalog cost carries only the 4 rate fields (input/output/cacheRead/
// cacheWrite) and never the runtime-only `total` field.
type imagesModelJSON struct {
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	API      string            `json:"api"`
	BaseURL  string            `json:"baseUrl,omitempty"`
	EnvKey   string            `json:"envKey,omitempty"`
	Input    []string          `json:"input,omitempty"`
	Output   []string          `json:"output,omitempty"`
	Cost     ModelCost         `json:"cost,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

func (m ImagesModel) MarshalJSON() ([]byte, error) {
	return json.Marshal(imagesModelJSON{
		Provider: m.Provider,
		ID:       m.ID,
		Name:     m.Name,
		API:      m.API,
		BaseURL:  m.BaseURL,
		EnvKey:   m.EnvKey,
		Input:    m.Input,
		Output:   m.Output,
		Cost: ModelCost{
			Input:      m.Cost.Input,
			Output:     m.Cost.Output,
			CacheRead:  m.Cost.CacheRead,
			CacheWrite: m.Cost.CacheWrite,
		},
		Headers: m.Headers,
	})
}

func (m *ImagesModel) UnmarshalJSON(data []byte) error {
	var raw imagesModelJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = ImagesModel{
		Provider: raw.Provider,
		ID:       raw.ID,
		Name:     raw.Name,
		API:      raw.API,
		BaseURL:  raw.BaseURL,
		EnvKey:   raw.EnvKey,
		Input:    raw.Input,
		Output:   raw.Output,
		Cost: Cost{
			Input:      raw.Cost.Input,
			Output:     raw.Cost.Output,
			CacheRead:  raw.Cost.CacheRead,
			CacheWrite: raw.Cost.CacheWrite,
		},
		Headers: raw.Headers,
	}
	return nil
}

type ImagesContext struct {
	Prompt string         `json:"prompt,omitempty"`
	Input  []ContentBlock `json:"input,omitempty"`
	Size   string         `json:"size,omitempty"`
	Count  int            `json:"count,omitempty"`
}

type ImagesOptions struct {
	APIKey          string                                               `json:"apiKey,omitempty"`
	Headers         map[string]string                                    `json:"headers,omitempty"`
	Signal          context.Context                                      `json:"-"`
	OnPayload       func(payload any, model ImagesModel) (any, error)    `json:"-"`
	OnResponse      func(resp ProviderResponse, model ImagesModel) error `json:"-"`
	TimeoutMs       int                                                  `json:"timeoutMs,omitempty"`
	MaxRetries      int                                                  `json:"maxRetries,omitempty"`
	MaxRetryDelayMs int                                                  `json:"maxRetryDelayMs,omitempty"`
	Metadata        map[string]any                                       `json:"metadata,omitempty"`
}

// Context derives the request context from the parent ctx and the optional
// options.Signal. The returned CancelFunc must be called by the caller (it is
// never nil) to release the child context, the deadline timer, and any
// context.AfterFunc registration created while combining the two sources.
func (options ImagesOptions) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil {
		return combineContexts(ctx, options.Signal)
	}
	if options.Signal != nil {
		return options.Signal, func() {}
	}
	return context.Background(), func() {}
}

func combineContexts(parent, signal context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if signal == nil {
		return parent, func() {}
	}
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if signalDeadline, ok := signal.Deadline(); ok {
		if parentDeadline, parentOK := parent.Deadline(); !parentOK || signalDeadline.Before(parentDeadline) {
			ctx, cancel = context.WithDeadline(parent, signalDeadline)
		}
	}
	if ctx == nil {
		ctx, cancel = context.WithCancel(parent)
	}
	stopSignal := context.AfterFunc(signal, cancel)
	// Returned to the caller so a successful request can release the child
	// context and unregister the signal watcher instead of leaking them until
	// the parent is cancelled.
	return ctx, func() {
		stopSignal()
		cancel()
	}
}

type GeneratedImage struct {
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type AssistantImages struct {
	API          string           `json:"api,omitempty"`
	Provider     string           `json:"provider,omitempty"`
	Model        string           `json:"model,omitempty"`
	Output       []ContentBlock   `json:"output,omitempty"`
	Images       []GeneratedImage `json:"images,omitempty"`
	StopReason   string           `json:"stopReason,omitempty"`
	Timestamp    int64            `json:"timestamp,omitempty"`
	ResponseID   string           `json:"responseId,omitempty"`
	Usage        Usage            `json:"usage,omitempty"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
}

type ImagesProvider interface {
	Generate(context.Context, ImagesModel, ImagesContext, ImagesOptions) (AssistantImages, error)
}

type registeredImagesProvider struct {
	provider ImagesProvider
	sourceID string
}

var (
	imageProviderMu sync.RWMutex
	imageProviders  = map[string]registeredImagesProvider{}
	imageModelMu    sync.RWMutex
	imageModels     = map[string]map[string]ImagesModel{}
)

func init() {
	RegisterImagesProvider("openrouter-images", OpenRouterImagesProvider{})
}

func RegisterImagesProvider(api string, provider ImagesProvider, sourceID ...string) {
	if api == "" || provider == nil {
		return
	}
	id := ""
	if len(sourceID) > 0 {
		id = sourceID[0]
	}
	imageProviderMu.Lock()
	defer imageProviderMu.Unlock()
	imageProviders[api] = registeredImagesProvider{provider: provider, sourceID: id}
}

func GetImagesProvider(api string) ImagesProvider {
	imageProviderMu.RLock()
	defer imageProviderMu.RUnlock()
	entry, ok := imageProviders[api]
	if !ok {
		return nil
	}
	return entry.provider
}

func UnregisterImagesProviders(sourceID string) {
	imageProviderMu.Lock()
	defer imageProviderMu.Unlock()
	for api, entry := range imageProviders {
		if entry.sourceID == sourceID {
			delete(imageProviders, api)
		}
	}
}

func ClearImagesProviders() {
	imageProviderMu.Lock()
	defer imageProviderMu.Unlock()
	imageProviders = map[string]registeredImagesProvider{}
}

func RegisterImageModel(model ImagesModel) {
	if model.Provider == "" || model.ID == "" {
		return
	}
	imageModelMu.Lock()
	defer imageModelMu.Unlock()
	providerModels := imageModels[model.Provider]
	if providerModels == nil {
		providerModels = map[string]ImagesModel{}
		imageModels[model.Provider] = providerModels
	}
	providerModels[model.ID] = cloneImagesModel(model)
}

func RegisterImageModels(models ...ImagesModel) {
	for _, model := range models {
		RegisterImageModel(model)
	}
}

func UnregisterImageModel(provider, modelID string) {
	imageModelMu.Lock()
	defer imageModelMu.Unlock()
	providerModels := imageModels[provider]
	if providerModels == nil {
		return
	}
	delete(providerModels, modelID)
	if len(providerModels) == 0 {
		delete(imageModels, provider)
	}
}

func GetImageModel(provider, modelID string) (ImagesModel, bool) {
	imageModelMu.RLock()
	defer imageModelMu.RUnlock()
	providerModels := imageModels[provider]
	if providerModels == nil {
		return ImagesModel{}, false
	}
	model, ok := providerModels[modelID]
	return cloneImagesModel(model), ok
}

func GetImageModels(provider string) []ImagesModel {
	imageModelMu.RLock()
	defer imageModelMu.RUnlock()
	providerModels := imageModels[provider]
	if providerModels == nil {
		return nil
	}
	models := make([]ImagesModel, 0, len(providerModels))
	for _, model := range providerModels {
		models = append(models, cloneImagesModel(model))
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models
}

func GetImageProviders() []string {
	imageModelMu.RLock()
	defer imageModelMu.RUnlock()
	providers := make([]string, 0, len(imageModels))
	for provider := range imageModels {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func GenerateImages(ctx context.Context, model ImagesModel, imageContext ImagesContext, options ImagesOptions) (AssistantImages, error) {
	ctx, cancel := options.Context(ctx)
	defer cancel()
	provider := GetImagesProvider(model.API)
	if provider == nil {
		return AssistantImages{}, fmt.Errorf("No API provider registered for api: %s", model.API) //nolint:staticcheck // ST1005: TS-faithful message (images.ts:9)
	}
	return provider.Generate(ctx, model, imageContext, options)
}

type OpenRouterImagesProvider struct {
	Client *http.Client
}

func (p OpenRouterImagesProvider) Generate(ctx context.Context, model ImagesModel, imageContext ImagesContext, options ImagesOptions) (AssistantImages, error) {
	output := AssistantImages{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: "stop",
		Timestamp:  time.Now().UnixMilli(),
	}
	apiKey := options.APIKey
	if apiKey == "" && model.EnvKey != "" {
		apiKey = os.Getenv(model.EnvKey)
	}
	if apiKey == "" {
		apiKey = GetEnvAPIKey(model.Provider)
	}
	if apiKey == "" {
		output.StopReason = "error"
		output.ErrorMessage = fmt.Sprintf("No API key available for provider: %s", model.Provider)
		return output, nil
	}
	requestBody, err := imageproviders.BuildOpenRouterPayload(model.ID, model.Output, openRouterImagesContext(imageContext))
	if err != nil {
		output.StopReason = "error"
		output.ErrorMessage = err.Error()
		return output, nil
	}
	var payload any = requestBody
	if options.OnPayload != nil {
		next, err := options.OnPayload(payload, model)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			return output, nil
		}
		if next != nil {
			payload = next
		}
	}
	baseURL := strings.TrimRight(model.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	client := p.Client
	if client == nil {
		client = providerSDKHTTPClient
	}
	body, err := aiproviders.DoOpenAISDKJSONWithClient(ctx, baseURL+"/chat/completions", apiKey, aiproviders.MergeHeaders(model.Headers, options.Headers), payload, true, client, imageRequestOptions(options, model))
	if err != nil {
		if ctx.Err() != nil {
			output.StopReason = "aborted"
		} else {
			output.StopReason = "error"
		}
		output.ErrorMessage = err.Error()
		return output, nil
	}
	parsed, err := imageproviders.ParseOpenRouterResponse(body)
	if err != nil {
		output.StopReason = "error"
		output.ErrorMessage = err.Error()
		return output, nil
	}
	output.ResponseID = parsed.ID
	output.Usage = parseOpenRouterImagesUsage(parsed.Usage, model)
	if strings.TrimSpace(parsed.Text) != "" {
		output.Output = append(output.Output, ContentBlock{Type: "text", Text: parsed.Text})
	}
	for _, image := range parsed.Images {
		output.Output = append(output.Output, ContentBlock{Type: "image", MimeType: image.MimeType, Data: image.Data})
		output.Images = append(output.Images, GeneratedImage{MimeType: image.MimeType, Data: image.Data})
	}
	return output, nil
}

func imageRequestOptions(options ImagesOptions, model ImagesModel) aiproviders.RequestOptions {
	return aiproviders.RequestOptions{
		TimeoutMs:       options.TimeoutMs,
		MaxRetries:      options.MaxRetries,
		UseMaxRetries:   options.MaxRetries != 0,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		OnResponse: func(resp aiproviders.ProviderResponse) error {
			if options.OnResponse == nil {
				return nil
			}
			return options.OnResponse(ProviderResponse{Status: resp.Status, Headers: resp.Headers}, model)
		},
	}
}

func cloneImagesModel(model ImagesModel) ImagesModel {
	model.Input = append([]string(nil), model.Input...)
	model.Output = append([]string(nil), model.Output...)
	if model.Headers != nil {
		headers := make(map[string]string, len(model.Headers))
		for key, value := range model.Headers {
			headers[key] = value
		}
		model.Headers = headers
	}
	return model
}

func openRouterImagesContext(imageContext ImagesContext) imageproviders.OpenRouterContext {
	input := make([]imageproviders.OpenRouterInputPart, 0, len(imageContext.Input))
	for _, item := range imageContext.Input {
		input = append(input, imageproviders.OpenRouterInputPart{
			Type:     item.Type,
			Text:     item.Text,
			MimeType: item.MimeType,
			Data:     item.Data,
		})
	}
	return imageproviders.OpenRouterContext{Prompt: imageContext.Prompt, Input: input}
}

func parseOpenRouterImagesUsage(raw imageproviders.OpenRouterUsage, model ImagesModel) Usage {
	promptTokens := raw.PromptTokens
	cacheWrite := raw.CacheWriteTokens
	reportedCached := raw.CachedTokens
	cacheRead := reportedCached
	if cacheWrite > 0 {
		cacheRead = aiproviders.MaxInt(0, reportedCached-cacheWrite)
	}
	input := aiproviders.MaxInt(0, promptTokens-cacheRead-cacheWrite)
	output := raw.CompletionTokens
	usage := Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + output + cacheRead + cacheWrite,
	}
	usage.Cost.Input = model.Cost.Input / 1_000_000 * float64(input)
	usage.Cost.Output = model.Cost.Output / 1_000_000 * float64(output)
	usage.Cost.CacheRead = model.Cost.CacheRead / 1_000_000 * float64(cacheRead)
	usage.Cost.CacheWrite = model.Cost.CacheWrite / 1_000_000 * float64(cacheWrite)
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage
}
