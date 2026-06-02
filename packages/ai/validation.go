package ai

import (
	"fmt"
	"strings"

	aiutils "github.com/guanshan/pi-go/packages/ai/utils"
)

type StringEnumOptions struct {
	Description string
	Default     string
}

func StringEnum(values []string, options ...StringEnumOptions) map[string]any {
	utilsOptions := make([]aiutils.StringEnumOptions, 0, len(options))
	for _, option := range options {
		utilsOptions = append(utilsOptions, aiutils.StringEnumOptions{
			Description: option.Description,
			Default:     option.Default,
		})
	}
	return aiutils.StringEnum(values, utilsOptions...)
}

func ValidateToolCall(tools []Tool, toolCall ToolCall) (map[string]any, error) {
	for _, tool := range tools {
		if tool.Name == toolCall.Name {
			return ValidateToolArgumentsWithSchema(tool, toolCall)
		}
	}
	return nil, fmt.Errorf("Tool %q not found", toolCall.Name)
}

func ValidateToolArgumentsWithSchema(tool Tool, toolCall ToolCall) (map[string]any, error) {
	args, err := aiutils.ParseToolArgumentValue(toolCall.Arguments)
	if err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: invalid JSON arguments: %w", toolCall.Name, err)
	}
	coerced, validationErrors := aiutils.ValidateJSONSchemaDetailed(args, tool.Parameters, "root")
	if len(validationErrors) == 0 {
		if object, ok := coerced.(map[string]any); ok {
			return object, nil
		}
		return nil, fmt.Errorf("Validation failed for tool %q:\n  - root: Expected object\n\nReceived arguments:\n%s", toolCall.Name, aiutils.PrettyJSON(args)) //nolint:staticcheck // TS-facing validation diagnostic keeps capitalization.
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Validation failed for tool %q:\n", toolCall.Name)
	for _, validationError := range validationErrors {
		builder.WriteString("  - ")
		builder.WriteString(validationError.Path)
		builder.WriteString(": ")
		builder.WriteString(validationError.Message)
		builder.WriteByte('\n')
	}
	builder.WriteString("\nReceived arguments:\n")
	builder.WriteString(aiutils.PrettyJSON(args))
	return nil, fmt.Errorf("%s", strings.TrimRight(builder.String(), "\n"))
}

func ValidateJSONSchema(value any, schema map[string]any) (any, error) {
	return aiutils.ValidateJSONSchema(value, schema)
}

func SortedSchemaKeys(schema map[string]any) []string {
	return aiutils.SortedSchemaKeys(schema)
}
