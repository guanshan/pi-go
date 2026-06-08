package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type scriptMessageRendererMetadata struct {
	CustomType string `json:"customType"`
}

func (r *scriptRuntime) RenderMessage(ctx context.Context, request MessageRenderRequest) (MessageRenderResult, error) {
	customType := strings.TrimSpace(request.CustomType)
	if customType == "" {
		return MessageRenderResult{}, errors.New("message renderer customType is empty")
	}
	request.CustomType = customType
	response, err := r.request(ctx, map[string]any{
		"type":       "render_message",
		"customType": customType,
		"request":    request,
	})
	if err != nil {
		return MessageRenderResult{}, err
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return MessageRenderResult{}, nil
	}
	var result MessageRenderResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return MessageRenderResult{}, fmt.Errorf("%s: invalid message renderer result for %s: %w", r.path, customType, err)
	}
	return result, nil
}
