package providers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/genai"
)

func GoogleThoughtSignature(signature []byte) string {
	if len(signature) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func GoogleVertexProjectLocation() (string, string, error) {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("GCLOUD_PROJECT")
	}
	if project == "" {
		return "", "", errors.New("set GOOGLE_CLOUD_PROJECT or GCLOUD_PROJECT for Google Vertex")
	}
	location := os.Getenv("GOOGLE_CLOUD_LOCATION")
	if location == "" {
		return "", "", errors.New("set GOOGLE_CLOUD_LOCATION for Google Vertex")
	}
	return project, location, nil
}

func HasGoogleVertexADC() bool {
	if !hasGoogleVertexProjectLocationEnv() {
		return false
	}
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		return fileExists(path)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return fileExists(filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"))
}

func hasGoogleVertexProjectLocationEnv() bool {
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if project == "" {
		project = os.Getenv("GCLOUD_PROJECT")
	}
	return project != "" && os.Getenv("GOOGLE_CLOUD_LOCATION") != ""
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func GoogleHTTPOptions(baseURL string, modelHeaders, headers map[string]string, vertex bool) genai.HTTPOptions {
	httpOptions := genai.HTTPOptions{}
	mergedHeaders := MergeHeaders(modelHeaders, headers)
	if len(mergedHeaders) > 0 {
		httpOptions.Headers = http.Header{}
		for key, value := range mergedHeaders {
			httpOptions.Headers.Set(key, value)
		}
	}
	if vertex {
		base := GoogleVertexCustomBaseURL(baseURL)
		if base != "" {
			httpOptions.BaseURL = base
			httpOptions.BaseURLResourceScope = genai.ResourceScopeCollection
			httpOptions.APIVersion = "v1"
			if BaseURLIncludesAPIVersion(base) {
				httpOptions.APIVersion = ""
			}
		}
		return httpOptions
	}
	if strings.TrimSpace(baseURL) != "" {
		httpOptions.BaseURL = strings.TrimRight(baseURL, "/")
		httpOptions.APIVersion = ""
	}
	return httpOptions
}

func GoogleThinkingConfig(level string, budgets ThinkingBudgets) *genai.ThinkingConfig {
	return GoogleThinkingConfigForModel("", level, budgets)
}

func GoogleThinkingConfigForModel(modelID, level string, budgets ThinkingBudgets) *genai.ThinkingConfig {
	if googleUsesThinkingLevel(modelID) {
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: googleThinkingLevelForModel(modelID, level)}
	}
	if modelID != "" {
		if budget := GoogleThinkingBudgetForModel(modelID, level, budgets); budget != 0 {
			return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: int32Ptr(budget)}
		}
	}
	if value := thinkingBudgetOverride(level, budgets); value > 0 {
		budget := int32(value)
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: int32Ptr(budget)}
	}
	switch level {
	case "minimal":
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: genai.ThinkingLevelMinimal}
	case "low":
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: genai.ThinkingLevelLow}
	case "medium":
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: genai.ThinkingLevelMedium}
	case "high", "xhigh":
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingLevel: genai.ThinkingLevelHigh}
	default:
		budget := int32(ThinkingBudgetWithBudgets(level, budgets))
		return &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: &budget}
	}
}

func GoogleDisabledThinkingConfig(modelID string) *genai.ThinkingConfig {
	switch {
	case isGemini3ProModel(modelID):
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow}
	case isGemini3FlashModel(modelID), isGemma4Model(modelID):
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMinimal}
	default:
		zero := int32(0)
		return &genai.ThinkingConfig{ThinkingBudget: &zero}
	}
}

func GoogleThinkingBudgetForModel(modelID, level string, budgets ThinkingBudgets) int32 {
	if value := thinkingBudgetOverride(level, budgets); value > 0 {
		return int32(value)
	}
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "2.5-pro"):
		switch level {
		case "minimal":
			return 128
		case "low":
			return 2048
		case "high", "xhigh":
			return 32768
		default:
			return 8192
		}
	case strings.Contains(id, "2.5-flash-lite"):
		switch level {
		case "minimal":
			return 512
		case "low":
			return 2048
		case "high", "xhigh":
			return 24576
		default:
			return 8192
		}
	case strings.Contains(id, "2.5-flash"):
		switch level {
		case "minimal":
			return 128
		case "low":
			return 2048
		case "high", "xhigh":
			return 24576
		default:
			return 8192
		}
	default:
		return -1
	}
}

func thinkingBudgetOverride(level string, budgets ThinkingBudgets) int {
	switch level {
	case "minimal":
		return budgets.Minimal
	case "low":
		return budgets.Low
	case "medium":
		return budgets.Medium
	case "high", "xhigh":
		return budgets.High
	default:
		return 0
	}
}

func googleUsesThinkingLevel(modelID string) bool {
	return isGemini3ProModel(modelID) || isGemini3FlashModel(modelID) || isGemma4Model(modelID)
}

func googleThinkingLevelForModel(modelID, level string) genai.ThinkingLevel {
	switch {
	case isGemini3ProModel(modelID):
		if level == "minimal" || level == "low" {
			return genai.ThinkingLevelLow
		}
		return genai.ThinkingLevelHigh
	case isGemma4Model(modelID):
		if level == "minimal" || level == "low" {
			return genai.ThinkingLevelMinimal
		}
		return genai.ThinkingLevelHigh
	default:
		switch level {
		case "minimal":
			return genai.ThinkingLevelMinimal
		case "low":
			return genai.ThinkingLevelLow
		case "medium":
			return genai.ThinkingLevelMedium
		default:
			return genai.ThinkingLevelHigh
		}
	}
}

func isGemini3ProModel(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "gemini-3") && strings.Contains(id, "pro")
}

func isGemini3FlashModel(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "gemini-3") && strings.Contains(id, "flash")
}

func isGemma4Model(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "gemma-4") || strings.Contains(id, "gemma4")
}

func int32Ptr(value int32) *int32 {
	return &value
}

type GoogleRequestOptions struct {
	ModelID         string
	SystemPrompt    string
	Messages        []GoogleMessage
	Tools           []map[string]any
	MaxTokens       int
	MaxOutput       int
	Temperature     *float64
	ToolChoice      any
	Reasoning       bool
	ThinkingLevel   string
	ThinkingBudgets ThinkingBudgets
	Vertex          bool
}

type GoogleMessage struct {
	Role       string
	Text       string
	ToolCallID string
	ToolName   string
	IsError    bool
	Blocks     []GoogleBlock
}

type GoogleBlock struct {
	Type              string
	Text              string
	Data              string
	MimeType          string
	Thinking          string
	ID                string
	Name              string
	Arguments         json.RawMessage
	TextSignature     string
	ThinkingSignature string
}

type GoogleParsed struct {
	Blocks     []GoogleBlock
	ToolCalls  []GoogleToolCall
	Usage      GoogleUsage
	StopReason string
}

type GoogleToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type GoogleUsage struct {
	Input       int
	Output      int
	CacheRead   int
	TotalTokens int
}

func GoogleGenerateContentConfig(options GoogleRequestOptions) *genai.GenerateContentConfig {
	config := &genai.GenerateContentConfig{}
	if strings.TrimSpace(options.SystemPrompt) != "" {
		config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: SanitizeProviderText(options.SystemPrompt)}}}
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = options.MaxOutput
	}
	if !options.Vertex && maxTokens < 1024 {
		maxTokens = 1024
	}
	if maxTokens > 0 {
		config.MaxOutputTokens = int32(maxTokens)
	}
	if options.Temperature != nil {
		value := float32(*options.Temperature)
		config.Temperature = &value
	}
	if len(options.Tools) > 0 {
		config.Tools = GoogleTools(options.Tools)
		if choice, ok := GoogleToolConfig(options.ToolChoice); ok {
			config.ToolConfig = choice
		}
	}
	if options.Reasoning {
		if options.ThinkingLevel != "" && options.ThinkingLevel != "off" {
			config.ThinkingConfig = GoogleThinkingConfigForModel(options.ModelID, options.ThinkingLevel, options.ThinkingBudgets)
		} else {
			config.ThinkingConfig = GoogleDisabledThinkingConfig(options.ModelID)
		}
	}
	return config
}

func GoogleToolConfig(choice any) (*genai.ToolConfig, bool) {
	var mode genai.FunctionCallingConfigMode
	switch ToolChoiceType(choice) {
	case "auto":
		mode = genai.FunctionCallingConfigModeAuto
	case "any", "required":
		mode = genai.FunctionCallingConfigModeAny
	case "none":
		mode = genai.FunctionCallingConfigModeNone
	default:
		return nil, false
	}
	return &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: mode}}, true
}

func GoogleContents(messages []GoogleMessage, modelID string) []*genai.Content {
	var out []*genai.Content
	for _, msg := range messages {
		role := genai.Role(genai.RoleUser)
		if msg.Role == "assistant" {
			role = genai.RoleModel
		}
		parts := []*genai.Part{}
		if msg.Role == "toolResult" {
			out = appendGoogleToolResultContent(out, msg, modelID)
			continue
		} else {
			for _, b := range msg.Blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						part := &genai.Part{Text: b.Text}
						if decoded := googleThoughtSignatureBytes(b.TextSignature); decoded != nil {
							part.ThoughtSignature = decoded
						}
						parts = append(parts, part)
					}
				case "thinking":
					if strings.TrimSpace(b.Thinking) != "" {
						part := &genai.Part{Text: b.Thinking, Thought: true}
						if decoded := googleThoughtSignatureBytes(b.ThinkingSignature); decoded != nil {
							part.ThoughtSignature = decoded
						}
						parts = append(parts, part)
					}
				case "image":
					data, err := base64.StdEncoding.DecodeString(b.Data)
					if err != nil {
						data = []byte(b.Data)
					}
					parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: b.MimeType, Data: data}})
				case "toolCall":
					var args map[string]any
					_ = json.Unmarshal(b.Arguments, &args)
					if args == nil {
						args = map[string]any{}
					}
					call := &genai.FunctionCall{Name: b.Name, Args: args}
					if GoogleRequiresToolCallID(modelID) {
						call.ID = GoogleNormalizeToolCallID(b.ID)
					}
					if decoded := googleThoughtSignatureBytes(b.ThinkingSignature); decoded != nil {
						parts = append(parts, &genai.Part{FunctionCall: call, ThoughtSignature: decoded})
					} else {
						parts = append(parts, &genai.Part{FunctionCall: call})
					}
				}
			}
		}
		if len(parts) > 0 {
			out = append(out, genai.NewContentFromParts(parts, role))
		}
	}
	return out
}

func appendGoogleToolResultContent(out []*genai.Content, msg GoogleMessage, modelID string) []*genai.Content {
	texts := []string{}
	images := []GoogleBlock{}
	for _, block := range msg.Blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				texts = append(texts, SanitizeProviderText(block.Text))
			}
		case "image":
			images = append(images, block)
		}
	}
	if len(msg.Blocks) == 0 && msg.Text != "" {
		texts = append(texts, SanitizeProviderText(msg.Text))
	}
	hasImages := len(images) > 0
	responseValue := strings.Join(texts, "\n")
	if responseValue == "" && hasImages {
		responseValue = "(see attached image)"
	}
	key := "output"
	if msg.IsError {
		key = "error"
	}
	functionResponse := &genai.FunctionResponse{
		Name:     msg.ToolName,
		Response: map[string]any{key: responseValue},
	}
	if GoogleRequiresToolCallID(modelID) {
		functionResponse.ID = GoogleNormalizeToolCallID(msg.ToolCallID)
	}
	imageParts := googleFunctionResponseImageParts(images)
	if hasImages && googleSupportsMultimodalFunctionResponse(modelID) {
		functionResponse.Parts = imageParts
	}
	part := &genai.Part{FunctionResponse: functionResponse}
	if len(out) > 0 && out[len(out)-1].Role == genai.RoleUser {
		last := out[len(out)-1]
		hasFunctionResponse := false
		for _, existing := range last.Parts {
			if existing.FunctionResponse != nil {
				hasFunctionResponse = true
				break
			}
		}
		if hasFunctionResponse {
			last.Parts = append(last.Parts, part)
		} else {
			out = append(out, genai.NewContentFromParts([]*genai.Part{part}, genai.RoleUser))
		}
	} else {
		out = append(out, genai.NewContentFromParts([]*genai.Part{part}, genai.RoleUser))
	}
	if hasImages && !googleSupportsMultimodalFunctionResponse(modelID) {
		followup := []*genai.Part{{Text: "Tool result image:"}}
		for _, image := range images {
			followup = append(followup, googleInlineImagePart(image))
		}
		out = append(out, genai.NewContentFromParts(followup, genai.RoleUser))
	}
	return out
}

func googleFunctionResponseImageParts(images []GoogleBlock) []*genai.FunctionResponsePart {
	parts := make([]*genai.FunctionResponsePart, 0, len(images))
	for _, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			data = []byte(image.Data)
		}
		parts = append(parts, genai.NewFunctionResponsePartFromBytes(data, image.MimeType))
	}
	return parts
}

func googleInlineImagePart(block GoogleBlock) *genai.Part {
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil {
		data = []byte(block.Data)
	}
	return &genai.Part{InlineData: &genai.Blob{MIMEType: block.MimeType, Data: data}}
}

func googleSupportsMultimodalFunctionResponse(modelID string) bool {
	id := strings.ToLower(modelID)
	if strings.HasPrefix(id, "gemini-") {
		parts := strings.Split(strings.TrimPrefix(id, "gemini-"), "-")
		if len(parts) > 0 && strings.HasPrefix(parts[0], "3") {
			return true
		}
		return false
	}
	return true
}

func GoogleRequiresToolCallID(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.HasPrefix(id, "claude-") || strings.HasPrefix(id, "gpt-oss-")
}

func GoogleNormalizeToolCallID(id string) string {
	if id == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() >= 64 {
			break
		}
	}
	return builder.String()
}

func googleThoughtSignatureBytes(signature string) []byte {
	if !googleValidThoughtSignature(signature) {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return nil
	}
	return decoded
}

func googleValidThoughtSignature(signature string) bool {
	if signature == "" || len(signature)%4 != 0 {
		return false
	}
	for _, r := range signature {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '=':
		default:
			return false
		}
	}
	return true
}

func GoogleTools(defs []map[string]any) []*genai.Tool {
	declarations := make([]*genai.FunctionDeclaration, 0, len(defs))
	for _, d := range defs {
		name, _ := d["name"].(string)
		description, _ := d["description"].(string)
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:                 name,
			Description:          description,
			ParametersJsonSchema: d["parameters"],
		})
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

func ParseGoogleGenerateContentResponse(resp *genai.GenerateContentResponse) (GoogleParsed, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil || resp.Candidates[0].Content == nil {
		return GoogleParsed{}, errors.New("empty Google response")
	}
	var blocks []GoogleBlock
	var calls []GoogleToolCall
	for _, part := range resp.Candidates[0].Content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			signature := GoogleThoughtSignature(part.ThoughtSignature)
			if part.Thought {
				blocks = append(blocks, GoogleBlock{Type: "thinking", Thinking: part.Text, ThinkingSignature: signature})
			} else {
				blocks = append(blocks, GoogleBlock{Type: "text", Text: part.Text, TextSignature: signature})
			}
		}
		if part.FunctionCall != nil {
			id := part.FunctionCall.ID
			if id == "" {
				id = ShortID()
			}
			argsMap := part.FunctionCall.Args
			if argsMap == nil {
				argsMap = map[string]any{}
			}
			args, err := json.Marshal(argsMap)
			if err != nil {
				return GoogleParsed{}, err
			}
			blocks = append(blocks, GoogleBlock{Type: "toolCall", ID: id, Name: part.FunctionCall.Name, Arguments: args, ThinkingSignature: GoogleThoughtSignature(part.ThoughtSignature)})
			calls = append(calls, GoogleToolCall{ID: id, Name: part.FunctionCall.Name, Arguments: args})
		}
	}
	stop := GoogleStopReason(string(resp.Candidates[0].FinishReason))
	if len(calls) > 0 {
		stop = "toolUse"
	}
	return GoogleParsed{
		Blocks:     blocks,
		ToolCalls:  calls,
		Usage:      GoogleUsageFromMetadata(resp.UsageMetadata),
		StopReason: stop,
	}, nil
}

func GoogleUsageFromMetadata(metadata *genai.GenerateContentResponseUsageMetadata) GoogleUsage {
	if metadata == nil {
		return GoogleUsage{}
	}
	cacheRead := int(metadata.CachedContentTokenCount)
	input := int(metadata.PromptTokenCount) - cacheRead
	if input < 0 {
		input = 0
	}
	return GoogleUsage{
		Input:       input,
		Output:      int(metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount),
		CacheRead:   cacheRead,
		TotalTokens: int(metadata.TotalTokenCount),
	}
}
