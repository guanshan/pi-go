package core

import (
	"bytes"
	"encoding/json"
)

var (
	jsonEscapeLineSeparator      = []byte(`\u2028`)
	jsonEscapeParagraphSeparator = []byte(`\u2029`)
	jsonLineSeparatorUTF8        = []byte{0xe2, 0x80, 0xa8}
	jsonParagraphSeparatorUTF8   = []byte{0xe2, 0x80, 0xa9}
)

// marshalJSONStringifyLine mirrors `${JSON.stringify(value)}\n` for JSONL
// writers. encoding/json can disable HTML escaping for <, >, and &, but it still
// hard-codes U+2028/U+2029 as \u escapes; JSON.stringify leaves them literal.
func marshalJSONStringifyLine(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return restoreJSONStringifySeparators(buf.Bytes()), nil
}

func marshalJSONNoHTMLEscape(value any) ([]byte, error) {
	raw, err := marshalJSONStringifyLine(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	return raw, nil
}

func restoreJSONStringifySeparators(raw []byte) []byte {
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

func appendJSONFieldBeforeClose(data []byte, field string) []byte {
	if len(data) < 2 || data[len(data)-1] != '}' {
		return data
	}
	out := make([]byte, 0, len(data)+len(field)+1)
	out = append(out, data[:len(data)-1]...)
	out = append(out, ',')
	out = append(out, field...)
	out = append(out, '}')
	return out
}

func insertJSONFieldAfterKey(data []byte, key, field string) []byte {
	marker := []byte(`"` + key + `":`)
	idx := bytes.Index(data, marker)
	if idx < 0 {
		return appendJSONFieldBeforeClose(data, field)
	}
	i := idx + len(marker)
	end := scanJSONValueEnd(data, i)
	if end < 0 {
		return appendJSONFieldBeforeClose(data, field)
	}
	out := make([]byte, 0, len(data)+len(field)+1)
	out = append(out, data[:end]...)
	out = append(out, ',')
	out = append(out, field...)
	out = append(out, data[end:]...)
	return out
}

func scanJSONValueEnd(data []byte, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(data); i++ {
		c := data[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i
			}
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
