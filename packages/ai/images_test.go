package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	imageproviders "github.com/guanshan/pi-go/packages/ai/providers/images"
)

func TestOpenRouterImagesProvider(t *testing.T) {
	var payload imageproviders.OpenRouterPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization=%q", got)
		}
		if got := r.Header.Get("X-Test"); got != "yes" {
			t.Fatalf("header=%q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{
			"id":"img-1",
			"usage":{"prompt_tokens":12,"completion_tokens":34,"prompt_tokens_details":{"cached_tokens":0}},
			"choices":[{"message":{"content":"Here is your image.","images":[{"image_url":"data:image/png;base64,ZmFrZS1wbmc="}]}}]
		}`))
	}))
	defer server.Close()

	model := ImagesModel{
		ID:       "google/gemini-image",
		API:      "openrouter-images",
		Provider: "openrouter",
		BaseURL:  server.URL,
		Output:   []string{"text", "image"},
		Cost:     Cost{Input: 0.015, Output: 0.03},
		Headers:  map[string]string{"X-Test": "yes"},
	}
	result, err := GenerateImages(context.Background(), model, ImagesContext{
		Input: []ContentBlock{{Type: "text", Text: "Generate a dog"}},
	}, ImagesOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "stop" || result.ResponseID != "img-1" {
		t.Fatalf("result=%#v", result)
	}
	if len(result.Output) != 2 || result.Output[0].Text != "Here is your image." || result.Output[1].MimeType != "image/png" {
		t.Fatalf("output=%#v", result.Output)
	}
	if len(result.Images) != 1 || result.Images[0].Data != "ZmFrZS1wbmc=" {
		t.Fatalf("images=%#v", result.Images)
	}
	if result.Usage.Input != 12 || result.Usage.Output != 34 || result.Usage.TotalTokens != 46 {
		t.Fatalf("usage=%#v", result.Usage)
	}
	if payload.Model != model.ID || payload.Stream {
		t.Fatalf("payload=%#v", payload)
	}
	if len(payload.Modalities) != 2 || payload.Modalities[0] != "image" || payload.Modalities[1] != "text" {
		t.Fatalf("modalities=%#v", payload.Modalities)
	}
	if payload.Messages[0].Content[0].Type != "text" || payload.Messages[0].Content[0].Text != "Generate a dog" {
		t.Fatalf("content=%#v", payload.Messages[0].Content)
	}
}

func TestGeneratedImageModelRegistry(t *testing.T) {
	providers := GetImageProviders()
	if len(providers) != 1 || providers[0] != "openrouter" {
		t.Fatalf("providers=%#v", providers)
	}
	models := GetImageModels("openrouter")
	if len(models) != 29 {
		t.Fatalf("openrouter image model count=%d", len(models))
	}
	model, ok := GetImageModel("openrouter", "google/gemini-2.5-flash-image")
	if !ok {
		t.Fatal("missing generated Gemini image model")
	}
	if model.API != "openrouter-images" || model.BaseURL != "https://openrouter.ai/api/v1" || model.EnvKey != "OPENROUTER_API_KEY" {
		t.Fatalf("model metadata=%#v", model)
	}
	if len(model.Input) != 2 || len(model.Output) != 2 || model.Cost.Input != 0.3 || model.Cost.Output != 2.5 {
		t.Fatalf("model details=%#v", model)
	}
}

func TestOpenRouterImagesProviderImageInputAndErrors(t *testing.T) {
	var payload imageproviders.OpenRouterPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"images":[{"image_url":{"url":"data:image/jpeg;base64,anBn"}}]}}]}`))
	}))
	defer server.Close()
	model := ImagesModel{ID: "image-model", API: "openrouter-images", Provider: "openrouter", BaseURL: server.URL, Output: []string{"image"}}
	result, err := GenerateImages(context.Background(), model, ImagesContext{
		Input: []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}},
	}, ImagesOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Modalities) != 1 || payload.Modalities[0] != "image" {
		t.Fatalf("modalities=%#v", payload.Modalities)
	}
	if payload.Messages[0].Content[0].Type != "image_url" || payload.Messages[0].Content[0].ImageURL.URL != "data:image/png;base64,abc" {
		t.Fatalf("content=%#v", payload.Messages[0].Content[0])
	}
	if len(result.Images) != 1 || result.Images[0].MimeType != "image/jpeg" || result.Images[0].Data != "anBn" {
		t.Fatalf("result=%#v", result)
	}

	missingKey, err := GenerateImages(context.Background(), model, ImagesContext{Prompt: "hi"}, ImagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if missingKey.StopReason != "error" || missingKey.ErrorMessage == "" {
		t.Fatalf("missing key result=%#v", missingKey)
	}
	emptyInput, err := GenerateImages(context.Background(), model, ImagesContext{}, ImagesOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if emptyInput.StopReason != "error" || emptyInput.ErrorMessage == "" {
		t.Fatalf("empty input result=%#v", emptyInput)
	}
}

func TestImagesProviderRegistrationAndModelCatalog(t *testing.T) {
	provider := recordingImagesProvider{}
	RegisterImagesProvider("unit-images", provider, "unit-images-test")
	defer UnregisterImagesProviders("unit-images-test")

	model := ImagesModel{Provider: "unit-provider", ID: "unit-model", API: "unit-images", Input: []string{"text"}, Output: []string{"image"}}
	RegisterImageModel(model)
	defer UnregisterImageModel(model.Provider, model.ID)

	got, ok := GetImageModel(model.Provider, model.ID)
	if !ok || got.API != "unit-images" {
		t.Fatalf("model=%#v ok=%v", got, ok)
	}
	models := GetImageModels(model.Provider)
	if len(models) != 1 || models[0].ID != model.ID {
		t.Fatalf("models=%#v", models)
	}

	result, err := GenerateImages(context.Background(), got, ImagesContext{Prompt: "paint"}, ImagesOptions{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != model.Provider || len(result.Images) != 1 || result.Images[0].Data != "unit" {
		t.Fatalf("result=%#v", result)
	}
}

func TestImageRegistryConcurrentAccess(t *testing.T) {
	RegisterImagesProvider("concurrent-images", recordingImagesProvider{}, "concurrent-images-test")
	defer UnregisterImagesProviders("concurrent-images-test")

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			model := ImagesModel{
				Provider: "concurrent-provider",
				ID:       fmt.Sprintf("model-%02d", i),
				API:      "concurrent-images",
				Input:    []string{"text"},
				Output:   []string{"image"},
			}
			RegisterImageModel(model)
			defer UnregisterImageModel(model.Provider, model.ID)
			if _, ok := GetImageModel(model.Provider, model.ID); !ok {
				t.Errorf("missing model %s", model.ID)
			}
			_ = GetImageModels("openrouter")
			_ = GetImageProviders()
			_ = GetImagesProvider("concurrent-images")
		}()
	}
	wg.Wait()
}

// TestGenerateImagesReleasesCombinedContext verifies the child context built
// from the parent ctx and options.Signal is cancelled when GenerateImages
// returns, so long-lived processes don't accumulate context/AfterFunc refs.
func TestGenerateImagesReleasesCombinedContext(t *testing.T) {
	provider := &ctxCapturingImagesProvider{}
	RegisterImagesProvider("ctx-images", provider, "ctx-images-test")
	defer UnregisterImagesProviders("ctx-images-test")
	model := ImagesModel{Provider: "ctx-provider", ID: "ctx-model", API: "ctx-images"}

	signal, cancelSignal := context.WithCancel(context.Background())
	defer cancelSignal()
	if _, err := GenerateImages(context.Background(), model, ImagesContext{Prompt: "hi"}, ImagesOptions{APIKey: "key", Signal: signal}); err != nil {
		t.Fatal(err)
	}
	// The parent was never cancelled, so only the deferred cancel can have
	// completed the captured child context.
	if provider.ctx == nil {
		t.Fatal("provider did not receive a context")
	}
	if provider.ctx.Err() == nil {
		t.Fatal("combined context should be cancelled after GenerateImages returns")
	}
}

// TestGenerateImagesSignalCancelsRequest verifies a cancelled options.Signal
// cancels the derived request context even while the provider runs.
func TestGenerateImagesSignalCancelsRequest(t *testing.T) {
	provider := &ctxCapturingImagesProvider{block: true, started: make(chan struct{})}
	RegisterImagesProvider("ctx-images", provider, "ctx-images-test")
	defer UnregisterImagesProviders("ctx-images-test")
	model := ImagesModel{Provider: "ctx-provider", ID: "ctx-model", API: "ctx-images"}

	signal, cancelSignal := context.WithCancel(context.Background())
	go func() {
		<-provider.started
		cancelSignal()
	}()
	if _, err := GenerateImages(context.Background(), model, ImagesContext{Prompt: "hi"}, ImagesOptions{APIKey: "key", Signal: signal}); err != nil {
		t.Fatal(err)
	}
	if provider.ctx.Err() == nil {
		t.Fatal("cancelled signal should cancel the derived request context")
	}
}

type ctxCapturingImagesProvider struct {
	ctx     context.Context
	block   bool
	started chan struct{}
}

func (p *ctxCapturingImagesProvider) Generate(ctx context.Context, model ImagesModel, _ ImagesContext, _ ImagesOptions) (AssistantImages, error) {
	p.ctx = ctx
	if p.block {
		close(p.started)
		<-ctx.Done()
	}
	return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: "stop"}, nil
}

type recordingImagesProvider struct{}

func (recordingImagesProvider) Generate(_ context.Context, model ImagesModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
	if options.APIKey == "" {
		return AssistantImages{StopReason: "error", ErrorMessage: "missing key"}, nil
	}
	return AssistantImages{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Images:     []GeneratedImage{{MimeType: "image/png", Data: "unit"}},
		StopReason: "stop",
	}, nil
}
