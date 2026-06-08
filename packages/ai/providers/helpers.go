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

// MarshalJSON serializes value the same way TypeScript's JSON.stringify does:
// it does NOT escape the HTML-significant characters < > &, nor U+2028/U+2029.
// Go's standard json.Marshal escapes these, which diverges from the TS upstream
// wire bytes and can break prompt-cache hashing on providers that hash the raw
// request body. Use this for every provider request body that is serialized by
// our own code (the OpenAI Go SDK already disables HTML escaping;
// Anthropic/Google paths rely on this helper or UnescapeJSONHTML).
//
// Like encoding/json, this rejects values that cannot be marshalled. Unlike
// Encoder.Encode it does not append a trailing newline, matching JSON.stringify.
func MarshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// Encoder.Encode always appends a single trailing '\n'; strip it so the
	// output matches json.Marshal / JSON.stringify byte-for-byte.
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return RestoreJSONStringifySeparators(out), nil
}

var (
	jsonEscapeLineSeparator      = []byte(`\u2028`)
	jsonEscapeParagraphSeparator = []byte(`\u2029`)
	jsonLineSeparatorUTF8        = []byte{0xe2, 0x80, 0xa8}
	jsonParagraphSeparatorUTF8   = []byte{0xe2, 0x80, 0xa9}
)

func RestoreJSONStringifySeparators(raw []byte) []byte {
	if !bytes.Contains(raw, jsonEscapeLineSeparator) && !bytes.Contains(raw, jsonEscapeParagraphSeparator) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		switch {
		case hasJSONEscapeAt(raw, i, jsonEscapeLineSeparator) && !jsonBackslashIsEscaped(raw, i):
			out = append(out, jsonLineSeparatorUTF8...)
			i += len(jsonEscapeLineSeparator)
		case hasJSONEscapeAt(raw, i, jsonEscapeParagraphSeparator) && !jsonBackslashIsEscaped(raw, i):
			out = append(out, jsonParagraphSeparatorUTF8...)
			i += len(jsonEscapeParagraphSeparator)
		default:
			out = append(out, raw[i])
			i++
		}
	}
	return out
}

func hasJSONEscapeAt(raw []byte, offset int, escape []byte) bool {
	return offset+len(escape) <= len(raw) && bytes.Equal(raw[offset:offset+len(escape)], escape)
}

func jsonBackslashIsEscaped(raw []byte, offset int) bool {
	backslashes := 0
	for i := offset - 1; i >= 0 && raw[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// UnescapeJSONHTML rewrites the <, > and & escape sequences that
// Go's encoding/json emits for the HTML-significant characters < > & back into
// their literal forms, matching what TypeScript's JSON.stringify produces. It is
// used on already-serialized JSON produced by third-party SDKs (Anthropic) whose
// internal encoder HTML-escapes by default and cannot be reconfigured.
//
// The scan is JSON-string aware: it only rewrites a sequence when the leading
// backslash is a genuine escape introducer inside a JSON string literal. A
// backslash that is itself escaped (e.g. the literal six-character text "<",
// serialized as \\u003c) is left untouched, so payloads that legitimately contain
// that text are not corrupted. The transform never changes the decoded value;
// "<" and "<" decode to the same character.
func UnescapeJSONHTML(data []byte) []byte {
	// Fast path: nothing to do if none of the escapes are present.
	if !bytes.Contains(data, []byte(`\u00`)) {
		return data
	}
	out := make([]byte, 0, len(data))
	inString := false
	for i := 0; i < len(data); {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			out = append(out, c)
			i++
			continue
		}
		// Inside a string literal.
		if c == '\\' {
			// Look at the escape sequence following the backslash.
			if i+5 < len(data) && data[i+1] == 'u' {
				switch string(data[i+1 : i+6]) {
				case "u003c", "u003C":
					out = append(out, '<')
					i += 6
					continue
				case "u003e", "u003E":
					out = append(out, '>')
					i += 6
					continue
				case "u0026":
					out = append(out, '&')
					i += 6
					continue
				}
			}
			// Any other escape (including \\ and \"): copy both bytes verbatim so
			// the second byte cannot be misread as a string terminator or as the
			// start of another escape.
			out = append(out, c)
			i++
			if i < len(data) {
				out = append(out, data[i])
				i++
			}
			continue
		}
		if c == '"' {
			inString = false
		}
		out = append(out, c)
		i++
	}
	return out
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

// SanitizeProviderText mirrors TS sanitizeSurrogates: it DELETES invalid/unpaired
// surrogate bytes (replaces them with the empty string) rather than inserting a
// U+FFFD replacement character, so the output matches upstream byte-for-byte on
// malformed (WTF-8) input.
func SanitizeProviderText(text string) string {
	return strings.ToValidUTF8(text, "")
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
