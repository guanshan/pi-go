package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type StringEnumOptions struct {
	Description string
	Default     string
}

type ValidationError struct {
	Path    string
	Message string
}

func StringEnum(values []string, options ...StringEnumOptions) map[string]any {
	schema := map[string]any{
		"type": "string",
		"enum": append([]string(nil), values...),
	}
	if len(options) > 0 {
		if options[0].Description != "" {
			schema["description"] = options[0].Description
		}
		if options[0].Default != "" {
			schema["default"] = options[0].Default
		}
	}
	return schema
}

func ValidateJSONSchema(value any, schema map[string]any) (any, error) {
	coerced, validationErrors := ValidateJSONSchemaDetailed(value, schema, "root")
	if len(validationErrors) == 0 {
		return coerced, nil
	}
	var builder strings.Builder
	for _, validationError := range validationErrors {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(validationError.Path)
		builder.WriteString(": ")
		builder.WriteString(validationError.Message)
	}
	return nil, fmt.Errorf("%s", builder.String())
}

func ValidateJSONSchemaDetailed(value any, schema map[string]any, path string) (any, []ValidationError) {
	return validateJSONSchema(value, schema, path)
}

func validateJSONSchema(value any, schema map[string]any, path string) (any, []ValidationError) {
	if schema == nil {
		return value, nil
	}
	next := cloneJSONValue(value)
	if nested, ok := schemaList(schema["allOf"]); ok {
		for _, child := range nested {
			var errs []ValidationError
			next, errs = validateJSONSchema(next, child, path)
			if len(errs) > 0 {
				return next, errs
			}
		}
	}
	if nested, ok := schemaList(schema["anyOf"]); ok {
		if candidate, ok := firstMatchingUnion(next, nested, path); ok {
			next = candidate
		}
	}
	if nested, ok := schemaList(schema["oneOf"]); ok {
		if candidate, ok := firstMatchingUnion(next, nested, path); ok {
			next = candidate
		}
	}
	types := schemaTypes(schema)
	if len(types) == 0 {
		if _, ok := schema["properties"]; ok {
			types = []string{"object"}
		} else if _, ok := schema["items"]; ok {
			types = []string{"array"}
		}
	}
	if len(types) > 0 && !matchesAnyJSONType(next, types) {
		for _, typ := range types {
			candidate := coercePrimitiveByType(next, typ)
			if !reflect.DeepEqual(candidate, next) {
				next = candidate
				break
			}
		}
	}
	var errs []ValidationError
	if expected, ok := schema["const"]; ok && !jsonValuesEqual(next, expected) {
		errs = append(errs, ValidationError{Path: path, Message: "Expected constant value " + valueForMessage(expected)})
	}
	if enumValues := enumList(schema["enum"]); len(enumValues) > 0 {
		matched := false
		for _, enumValue := range enumValues {
			if jsonValuesEqual(next, enumValue) {
				matched = true
				break
			}
		}
		if !matched {
			errs = append(errs, ValidationError{Path: path, Message: "Expected one of " + valueForMessage(enumValues)})
		}
	}
	if len(types) > 0 && !matchesAnyJSONType(next, types) {
		errs = append(errs, ValidationError{Path: path, Message: "Expected " + strings.Join(types, " or ")})
		return next, errs
	}
	if contains(types, "object") {
		object, ok := next.(map[string]any)
		if !ok {
			errs = append(errs, ValidationError{Path: path, Message: "Expected object"})
			return next, errs
		}
		nextObject, objectErrors := validateObjectSchema(object, schema, path)
		next = nextObject
		errs = append(errs, objectErrors...)
	}
	if contains(types, "array") {
		array, ok := next.([]any)
		if !ok {
			errs = append(errs, ValidationError{Path: path, Message: "Expected array"})
			return next, errs
		}
		nextArray, arrayErrors := validateArraySchema(array, schema, path)
		next = nextArray
		errs = append(errs, arrayErrors...)
	}
	return next, errs
}

func validateObjectSchema(object map[string]any, schema map[string]any, path string) (map[string]any, []ValidationError) {
	out := cloneJSONObject(object)
	var errs []ValidationError
	properties := schemaProperties(schema["properties"])
	for _, required := range stringList(schema["required"]) {
		if _, ok := out[required]; !ok {
			errs = append(errs, ValidationError{Path: joinPath(path, required), Message: "Expected required property"})
		}
	}
	for key, childSchema := range properties {
		if value, ok := out[key]; ok {
			coerced, childErrors := validateJSONSchema(value, childSchema, joinPath(path, key))
			out[key] = coerced
			errs = append(errs, childErrors...)
		}
	}
	defined := map[string]bool{}
	for key := range properties {
		defined[key] = true
	}
	switch additional := schema["additionalProperties"].(type) {
	case bool:
		if !additional {
			for key := range out {
				if !defined[key] {
					errs = append(errs, ValidationError{Path: joinPath(path, key), Message: "Unexpected property"})
				}
			}
		}
	default:
		additionalSchema, ok := toSchemaMap(additional)
		if !ok {
			break
		}
		for key, value := range out {
			if defined[key] {
				continue
			}
			coerced, childErrors := validateJSONSchema(value, additionalSchema, joinPath(path, key))
			out[key] = coerced
			errs = append(errs, childErrors...)
		}
	}
	return out, errs
}

func validateArraySchema(array []any, schema map[string]any, path string) ([]any, []ValidationError) {
	out := append([]any(nil), array...)
	var errs []ValidationError
	switch items := schema["items"].(type) {
	default:
		if itemSchema, ok := toSchemaMap(items); ok {
			for i, value := range out {
				coerced, childErrors := validateJSONSchema(value, itemSchema, joinPath(path, strconv.Itoa(i)))
				out[i] = coerced
				errs = append(errs, childErrors...)
			}
		}
	case map[string]any:
		for i, value := range out {
			coerced, childErrors := validateJSONSchema(value, items, joinPath(path, strconv.Itoa(i)))
			out[i] = coerced
			errs = append(errs, childErrors...)
		}
	case []any:
		for i, itemSchema := range items {
			if i >= len(out) {
				break
			}
			childSchema, ok := itemSchema.(map[string]any)
			if !ok {
				continue
			}
			coerced, childErrors := validateJSONSchema(out[i], childSchema, joinPath(path, strconv.Itoa(i)))
			out[i] = coerced
			errs = append(errs, childErrors...)
		}
	}
	return out, errs
}

func firstMatchingUnion(value any, schemas []map[string]any, path string) (any, bool) {
	for _, schema := range schemas {
		candidate, errs := validateJSONSchema(cloneJSONValue(value), schema, path)
		if len(errs) == 0 {
			return candidate, true
		}
	}
	return value, false
}

func coercePrimitiveByType(value any, typ string) any {
	switch typ {
	case "number":
		switch v := value.(type) {
		case nil:
			return float64(0)
		case string:
			if strings.TrimSpace(v) != "" {
				if parsed, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
					return parsed
				}
			}
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		}
	case "integer":
		switch v := value.(type) {
		case nil:
			return float64(0)
		case string:
			if strings.TrimSpace(v) != "" {
				if parsed, err := strconv.ParseFloat(v, 64); err == nil && math.Trunc(parsed) == parsed {
					return parsed
				}
			}
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		}
	case "boolean":
		switch v := value.(type) {
		case nil:
			return false
		case string:
			if v == "true" {
				return true
			}
			if v == "false" {
				return false
			}
		case float64:
			if v == 1 {
				return true
			}
			if v == 0 {
				return false
			}
		case json.Number:
			if v.String() == "1" {
				return true
			}
			if v.String() == "0" {
				return false
			}
		}
	case "string":
		switch v := value.(type) {
		case nil:
			return ""
		case float64:
			if math.Trunc(v) == v {
				return strconv.FormatInt(int64(v), 10)
			}
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			if v {
				return "true"
			}
			return "false"
		case json.Number:
			return v.String()
		}
	case "null":
		switch v := value.(type) {
		case string:
			if v == "" {
				return nil
			}
		case float64:
			if v == 0 {
				return nil
			}
		case bool:
			if !v {
				return nil
			}
		}
	}
	return value
}

func ParseToolArguments(raw json.RawMessage) (map[string]any, error) {
	value, err := ParseToolArgumentValue(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	return object, nil
}

func ParseToolArgumentValue(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return normalizeJSONNumbers(value), nil
}

func schemaTypes(schema map[string]any) []string {
	switch value := schema["type"].(type) {
	case string:
		return []string{value}
	case []string:
		return append([]string(nil), value...)
	case []any:
		var out []string
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func schemaList(value any) ([]map[string]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		schema, ok := toSchemaMap(item)
		if !ok {
			return nil, false
		}
		out = append(out, schema)
	}
	return out, true
}

func schemaProperties(value any) map[string]map[string]any {
	raw, ok := objectMap(value)
	if !ok {
		return nil
	}
	out := map[string]map[string]any{}
	for key, item := range raw {
		if schema, ok := toSchemaMap(item); ok {
			out[key] = schema
		}
	}
	return out
}

func enumList(value any) []any {
	switch v := value.(type) {
	case []any:
		return append([]any(nil), v...)
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case []int:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = float64(item)
		}
		return out
	default:
		return nil
	}
}

func objectMap(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false
	}
	return object, true
}

func toSchemaMap(value any) (map[string]any, bool) {
	return objectMap(value)
}

func stringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesAnyJSONType(value any, types []string) bool {
	for _, typ := range types {
		if matchesJSONType(value, typ) {
			return true
		}
	}
	return false
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "number":
		_, ok := asFloat(value)
		return ok
	case "integer":
		n, ok := asFloat(value)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func asFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return float64(i)
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = normalizeJSONNumbers(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeJSONNumbers(item)
		}
		return out
	default:
		return value
	}
}

func cloneJSONValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return value
	}
	return normalizeJSONNumbers(out)
}

func cloneJSONObject(value map[string]any) map[string]any {
	cloned, ok := cloneJSONValue(value).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return cloned
}

func jsonValuesEqual(left, right any) bool {
	return reflect.DeepEqual(normalizeJSONNumbers(left), normalizeJSONNumbers(right))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func joinPath(base, child string) string {
	if base == "" || base == "root" {
		return child
	}
	return base + "." + child
}

func valueForMessage(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func PrettyJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func SortedSchemaKeys(schema map[string]any) []string {
	keys := make([]string, 0, len(schema))
	for key := range schema {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
