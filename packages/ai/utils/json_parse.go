package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

func RepairJSON(input string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for _, r := range input {
		if !inString {
			b.WriteRune(r)
			if r == '"' {
				inString = true
			}
			continue
		}
		if escaped {
			if strings.ContainsRune(`"\/bfnrtu`, r) {
				b.WriteRune('\\')
				b.WriteRune(r)
			} else {
				b.WriteString(`\\`)
				b.WriteRune(r)
			}
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '"':
			b.WriteRune(r)
			inString = false
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r >= 0 && r <= 0x1f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	if escaped {
		b.WriteString(`\\`)
	}
	return b.String()
}

func ParseJSONWithRepair[T any](input string) (T, error) {
	var out T
	if err := json.Unmarshal([]byte(input), &out); err == nil {
		return out, nil
	}
	repaired := RepairJSON(input)
	if repaired != input {
		if err := json.Unmarshal([]byte(repaired), &out); err == nil {
			return out, nil
		}
	}
	return out, json.Unmarshal([]byte(input), &out)
}

func ParseStreamingJSON(input string) map[string]any {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}
	}
	if parsed, err := ParseJSONWithRepair[map[string]any](input); err == nil {
		return parsed
	}
	// The input is incomplete (mid-stream). Complete it to the most content we
	// can recover, mirroring the upstream `partial-json` behaviour: a value
	// truncated inside a string keeps the partial string, dangling
	// separators/keys are dropped, and open brackets are closed.
	if completed, ok := completePartialJSON(RepairJSON(input)); ok {
		var out map[string]any
		if json.Unmarshal([]byte(completed), &out) == nil {
			return out
		}
	}
	return map[string]any{}
}

// completePartialJSON takes a (possibly) truncated JSON document and returns the
// largest valid completion it can recover, plus whether completion succeeded.
//
// It scans the input tracking string/escape state and a stack of open
// containers. A value truncated inside a string is recovered by closing the
// quote (matching `partial-json`); a string that is actually a not-yet-finished
// object key is dropped instead. Otherwise it falls back to the last position
// where a complete value had been parsed and closes the open containers there,
// which discards trailing commas, dangling colons, and partial literals.
func completePartialJSON(s string) (string, bool) {
	var openers []byte // stack of '}' / ']' closers for currently open containers
	var bestLen = -1
	var bestClosers []byte
	inString := false
	escaped := false
	// expectKey is true when the current (top) container is an object and the
	// next string literal would be a key rather than a value.
	expectKey := false

	record := func(upto int) {
		bestLen = upto
		bestClosers = append(bestClosers[:0], openers...)
	}
	topIsObject := func() bool {
		return len(openers) > 0 && openers[len(openers)-1] == '}'
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
				if topIsObject() && expectKey {
					expectKey = false // a key just closed; a colon should follow
				} else {
					record(i + 1) // a value string just closed
				}
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			openers = append(openers, '}')
			expectKey = true
			record(i + 1) // an empty object is a valid completion
		case '[':
			openers = append(openers, ']')
			expectKey = false
			record(i + 1) // an empty array is a valid completion
		case '}', ']':
			if len(openers) > 0 {
				openers = openers[:len(openers)-1]
			}
			expectKey = topIsObject()
			record(i + 1)
		case ':':
			expectKey = false
		case ',':
			expectKey = topIsObject()
		case ' ', '\t', '\n', '\r':
			// structural whitespace, ignore
		default:
			// literal token: number, true, false, or null
			j := i
			for j < len(s) && isJSONLiteralByte(s[j]) {
				j++
			}
			// A literal followed by more input is complete. A literal that runs
			// to EOF is only kept if it already parses as a value (e.g. "1"),
			// so a partial token such as "tru" or "1." is dropped instead.
			if j < len(s) || json.Valid([]byte(s[i:j])) {
				record(j)
			}
			i = j - 1
		}
	}

	// Truncated inside a string value: close the quote and the open containers.
	if inString && (!topIsObject() || !expectKey) {
		cand := s + `"`
		for k := len(openers) - 1; k >= 0; k-- {
			cand += string(openers[k])
		}
		if json.Valid([]byte(cand)) {
			return cand, true
		}
	}
	if bestLen >= 0 {
		cand := s[:bestLen]
		for k := len(bestClosers) - 1; k >= 0; k-- {
			cand += string(bestClosers[k])
		}
		if json.Valid([]byte(cand)) {
			return cand, true
		}
	}
	return "", false
}

func isJSONLiteralByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9':
		return true
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	case b == '+' || b == '-' || b == '.':
		return true
	default:
		return false
	}
}

func StreamingToolArguments(raw string) json.RawMessage {
	if parsed := ParseStreamingJSON(raw); len(parsed) > 0 {
		if encoded, err := json.Marshal(parsed); err == nil {
			return encoded
		}
	}
	return json.RawMessage(`{}`)
}

func ParseSSEEvents(raw []byte) []map[string]any {
	chunks := strings.Split(string(raw), "\n\n")
	var events []map[string]any
	for _, chunk := range chunks {
		var dataLines []string
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(data), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

func RawJSONMap(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) == nil && v != nil {
		return v
	}
	return map[string]any{}
}
