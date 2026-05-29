package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateToolCallCoercesJSONSchemaArguments(t *testing.T) {
	tool := Tool{
		Name: "configure",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"count", "enabled", "name", "tags"},
			"properties": map[string]any{
				"count":   map[string]any{"type": "integer"},
				"enabled": map[string]any{"type": "boolean"},
				"name":    map[string]any{"type": "string"},
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"additionalProperties": map[string]any{"type": "number"},
		},
	}
	args, err := ValidateToolCall([]Tool{tool}, ToolCall{
		Name:      "configure",
		Arguments: json.RawMessage(`{"count":"3","enabled":"true","name":99,"tags":[1,true],"extra":"4.5"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if args["count"] != float64(3) || args["enabled"] != true || args["name"] != "99" || args["extra"] != float64(4.5) {
		t.Fatalf("args=%#v", args)
	}
	tags, ok := args["tags"].([]any)
	if !ok || tags[0] != "1" || tags[1] != "true" {
		t.Fatalf("tags=%#v", args["tags"])
	}
}

func TestValidateToolCallErrors(t *testing.T) {
	tool := Tool{
		Name: "strict",
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"path"},
			"properties":           map[string]any{"path": map[string]any{"type": "string"}},
			"additionalProperties": false,
		},
	}
	_, err := ValidateToolCall([]Tool{tool}, ToolCall{Name: "strict", Arguments: json.RawMessage(`{"other":1}`)})
	if err == nil {
		t.Fatal("expected validation error")
	}
	message := err.Error()
	if !strings.Contains(message, "path: Expected required property") || !strings.Contains(message, "other: Unexpected property") {
		t.Fatalf("message=%s", message)
	}
	_, err = ValidateToolCall([]Tool{tool}, ToolCall{Name: "missing", Arguments: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), `Tool "missing" not found`) {
		t.Fatalf("missing err=%v", err)
	}
}

func TestValidateJSONSchemaUnionAndEnum(t *testing.T) {
	schema := map[string]any{
		"oneOf": []any{
			map[string]any{"type": "integer"},
			StringEnum([]string{"auto", "manual"}, StringEnumOptions{Description: "mode", Default: "auto"}),
		},
	}
	got, err := ValidateJSONSchema("42", schema)
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(42) {
		t.Fatalf("got=%#v", got)
	}
	got, err = ValidateJSONSchema("manual", schema)
	if err != nil {
		t.Fatal(err)
	}
	if got != "manual" {
		t.Fatalf("enum got=%#v", got)
	}
	_, err = ValidateJSONSchema("other", StringEnum([]string{"auto", "manual"}))
	if err == nil || !strings.Contains(err.Error(), "Expected one of") {
		t.Fatalf("enum err=%v", err)
	}
}

func TestValidateJSONSchemaTupleItems(t *testing.T) {
	got, err := ValidateJSONSchema([]any{"1", 2}, map[string]any{
		"type": "array",
		"items": []any{
			map[string]any{"type": "integer"},
			map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	items := got.([]any)
	if items[0] != float64(1) || items[1] != "2" {
		t.Fatalf("items=%#v", items)
	}
}

func TestValidateToolCallNestedRequiredPathAndNonObjectArguments(t *testing.T) {
	tool := Tool{
		Name: "write",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"config"},
			"properties": map[string]any{
				"config": map[string]any{
					"type":     "object",
					"required": []string{"path"},
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	_, err := ValidateToolCall([]Tool{tool}, ToolCall{Name: "write", Arguments: json.RawMessage(`{"config":{}}`)})
	if err == nil {
		t.Fatal("expected nested validation error")
	}
	if message := err.Error(); !strings.Contains(message, "config.path: Expected required property") || !strings.Contains(message, "Received arguments:") {
		t.Fatalf("message=%s", message)
	}

	_, err = ValidateToolCall([]Tool{tool}, ToolCall{Name: "write", Arguments: json.RawMessage(`[]`)})
	if err == nil {
		t.Fatal("expected non-object validation error")
	}
	if message := err.Error(); !strings.Contains(message, "root: Expected object") || !strings.Contains(message, "[]") {
		t.Fatalf("message=%s", message)
	}
}

func TestValidateToolCallRejectsInvalidCoercions(t *testing.T) {
	tool := Tool{
		Name: "set",
		Parameters: map[string]any{
			"type":     "object",
			"required": []string{"enabled", "count"},
			"properties": map[string]any{
				"enabled": map[string]any{"type": "boolean"},
				"count":   map[string]any{"type": "integer"},
			},
		},
	}
	_, err := ValidateToolCall([]Tool{tool}, ToolCall{Name: "set", Arguments: json.RawMessage(`{"enabled":"1","count":"42.1"}`)})
	if err == nil {
		t.Fatal("expected invalid coercion error")
	}
	message := err.Error()
	if !strings.Contains(message, "enabled: Expected boolean") || !strings.Contains(message, "count: Expected integer") {
		t.Fatalf("message=%s", message)
	}
}

func TestUnmarshalToolArgumentsIsSeparateFromSchemaValidation(t *testing.T) {
	var target struct {
		Count int `json:"count"`
	}
	if err := UnmarshalToolArguments(json.RawMessage(`{"count":3}`), &target); err != nil {
		t.Fatal(err)
	}
	if target.Count != 3 {
		t.Fatalf("target=%#v", target)
	}

	tool := Tool{
		Name: "set",
		Parameters: map[string]any{
			"type":       "object",
			"required":   []string{"count"},
			"properties": map[string]any{"count": map[string]any{"type": "integer"}},
		},
	}
	_, err := ValidateToolCall([]Tool{tool}, ToolCall{Name: "set", Arguments: json.RawMessage(`{"count":"3.5"}`)})
	if err == nil || !strings.Contains(err.Error(), "count: Expected integer") {
		t.Fatalf("err=%v", err)
	}
}
