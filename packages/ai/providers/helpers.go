package providers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func ContentToString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case nil:
		return ""
	case []any:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		raw, _ := json.Marshal(c)
		return string(raw)
	}
}

func NormalizeToolArguments(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err == nil {
			encoded = strings.TrimSpace(encoded)
			if encoded != "" && json.Valid([]byte(encoded)) {
				return json.RawMessage(encoded)
			}
		}
		return json.RawMessage(`{}`)
	}
	if json.Valid(trimmed) {
		return append(json.RawMessage(nil), trimmed...)
	}
	return json.RawMessage(`{}`)
}

func ReasoningEffort(level string) string {
	switch level {
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "high", "xhigh":
		return "high"
	default:
		return "medium"
	}
}

type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
}

func ThinkingBudget(level string) int {
	return ThinkingBudgetWithBudgets(level, ThinkingBudgets{})
}

func ThinkingBudgetWithBudgets(level string, budgets ThinkingBudgets) int {
	switch level {
	case "minimal":
		if budgets.Minimal > 0 {
			return budgets.Minimal
		}
		return 1024
	case "low":
		if budgets.Low > 0 {
			return budgets.Low
		}
		return 2048
	case "high":
		if budgets.High > 0 {
			return budgets.High
		}
		return 8192
	case "xhigh":
		return 16384
	default:
		if budgets.Medium > 0 {
			return budgets.Medium
		}
		return 4096
	}
}

func ShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func DataURL(mimeType, data string) string {
	return "data:" + mimeType + ";base64," + data
}

func SanitizeProviderText(text string) string {
	return strings.ToValidUTF8(text, "\uFFFD")
}

func ParseDataURLImage(value string) (mimeType string, data string, ok bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(value, "data:")
	mimeType, data, found := strings.Cut(rest, ";base64,")
	if !found || mimeType == "" || data == "" {
		return "", "", false
	}
	return mimeType, data, true
}

func StringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ToolChoiceType(choice any) string {
	switch value := choice.(type) {
	case string:
		return value
	case map[string]any:
		if typ, _ := value["type"].(string); typ != "" {
			return typ
		}
	case map[string]string:
		if typ := value["type"]; typ != "" {
			return typ
		}
	}
	return ""
}

func ToolChoiceName(choice any) string {
	switch value := choice.(type) {
	case map[string]any:
		if name, _ := value["name"].(string); name != "" {
			return name
		}
		if fn, ok := value["function"].(map[string]any); ok {
			if name, _ := fn["name"].(string); name != "" {
				return name
			}
		}
	case map[string]string:
		if name := value["name"]; name != "" {
			return name
		}
	}
	return ""
}

func OpenAIToolChoice(choice any) any {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		return value
	case map[string]any, map[string]string:
		typ := ToolChoiceType(value)
		name := ToolChoiceName(value)
		if name != "" && (typ == "tool" || typ == "function" || typ == "") {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
	}
	return choice
}

func MistralToolChoice(choice any) any {
	switch value := choice.(type) {
	case nil:
		return nil
	case string:
		return value
	case map[string]any, map[string]string:
		name := ToolChoiceName(value)
		if name != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}
		}
	}
	return choice
}
