package imageproviders

import (
	"encoding/json"
	"fmt"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type OpenRouterInputPart struct {
	Type     string
	Text     string
	MimeType string
	Data     string
}

type OpenRouterContext struct {
	Prompt string
	Input  []OpenRouterInputPart
}

type OpenRouterPayload struct {
	Model      string              `json:"model"`
	Messages   []OpenRouterMessage `json:"messages"`
	Stream     bool                `json:"stream"`
	Modalities []string            `json:"modalities"`
}

type OpenRouterMessage struct {
	Role    string                  `json:"role"`
	Content []OpenRouterContentPart `json:"content"`
}

type OpenRouterContentPart struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	ImageURL *OpenRouterImageURL `json:"image_url,omitempty"`
}

type OpenRouterImageURL struct {
	URL string `json:"url"`
}

type OpenRouterResponse struct {
	ID     string
	Text   string
	Images []OpenRouterGeneratedImage
	Usage  OpenRouterUsage
}

type OpenRouterGeneratedImage struct {
	MimeType string
	Data     string
}

type OpenRouterUsage struct {
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	CacheWriteTokens int
}

func BuildOpenRouterPayload(modelID string, output []string, context OpenRouterContext) (OpenRouterPayload, error) {
	input := append([]OpenRouterInputPart(nil), context.Input...)
	if len(input) == 0 && context.Prompt != "" {
		input = append(input, OpenRouterInputPart{Type: "text", Text: context.Prompt})
	}
	if len(input) == 0 {
		return OpenRouterPayload{}, fmt.Errorf("images context input is empty")
	}
	parts := make([]OpenRouterContentPart, 0, len(input))
	for _, item := range input {
		switch item.Type {
		case "text":
			parts = append(parts, OpenRouterContentPart{Type: "text", Text: aiutils.SanitizeUnicode(item.Text)})
		case "image":
			if item.MimeType == "" || item.Data == "" {
				return OpenRouterPayload{}, fmt.Errorf("image input requires mimeType and data")
			}
			parts = append(parts, OpenRouterContentPart{
				Type:     "image_url",
				ImageURL: &OpenRouterImageURL{URL: aiproviders.DataURL(item.MimeType, item.Data)},
			})
		default:
			return OpenRouterPayload{}, fmt.Errorf("unsupported image input type %q", item.Type)
		}
	}
	modalities := []string{"image"}
	if aiproviders.StringSliceContains(output, "text") {
		modalities = []string{"image", "text"}
	}
	return OpenRouterPayload{
		Model:      modelID,
		Messages:   []OpenRouterMessage{{Role: "user", Content: parts}},
		Stream:     false,
		Modalities: modalities,
	}, nil
}

func ParseOpenRouterResponse(body []byte) (OpenRouterResponse, error) {
	var parsed openRouterImageGenerationResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return OpenRouterResponse{}, err
	}
	response := OpenRouterResponse{
		ID: parsed.ID,
		Usage: OpenRouterUsage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
			CachedTokens:     parsed.Usage.PromptTokensDetails.CachedTokens,
			CacheWriteTokens: parsed.Usage.PromptTokensDetails.CacheWriteTokens,
		},
	}
	if len(parsed.Choices) == 0 {
		return response, nil
	}
	message := parsed.Choices[0].Message
	response.Text = message.Content
	for _, image := range message.Images {
		url := image.ImageURL
		if image.ImageURLObject.URL != "" {
			url = image.ImageURLObject.URL
		}
		mimeType, data, ok := aiproviders.ParseDataURLImage(url)
		if !ok {
			continue
		}
		response.Images = append(response.Images, OpenRouterGeneratedImage{MimeType: mimeType, Data: data})
	}
	return response, nil
}

type openRouterImageGenerationResponse struct {
	ID      string                         `json:"id"`
	Usage   openRouterImageGenerationUsage `json:"usage"`
	Choices []openRouterImageChoice        `json:"choices"`
}

type openRouterImageGenerationUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

type openRouterImageChoice struct {
	Message openRouterImageMessage `json:"message"`
}

type openRouterImageMessage struct {
	Content string                     `json:"content"`
	Images  []openRouterGeneratedImage `json:"images"`
}

type openRouterGeneratedImage struct {
	ImageURL       string             `json:"image_url"`
	ImageURLObject OpenRouterImageURL `json:"image_url_object"`
}

func (i *openRouterGeneratedImage) UnmarshalJSON(data []byte) error {
	var raw struct {
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.ImageURL) == 0 {
		return nil
	}
	var asString string
	if err := json.Unmarshal(raw.ImageURL, &asString); err == nil {
		i.ImageURL = asString
		return nil
	}
	var asObject OpenRouterImageURL
	if err := json.Unmarshal(raw.ImageURL, &asObject); err == nil {
		i.ImageURLObject = asObject
		return nil
	}
	return nil
}
