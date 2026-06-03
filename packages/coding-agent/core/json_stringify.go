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
