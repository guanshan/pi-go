package session

import (
	"bytes"
	"context"
	"encoding/json"
)

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// marshalNoHTMLEscape serializes value to JSON with JSON.stringify byte
// semantics: compact JSON with no HTML escaping and literal U+2028/U+2029.
//
// Go's stdlib json.Marshal HTML-escapes those runes (e.g. < -> <), but TS
// JSON.stringify (used by jsonl-storage.ts to write session files) never does.
// Session content routinely contains <, >, and & (HTML tags, &&, List<String>,
// a < b), so the default escaping makes Go-written session files differ
// byte-for-byte from TS-written ones. We disable HTML escaping to match TS.
//
// The returned bytes carry no trailing newline, matching json.Marshal semantics
// (json.Encoder.Encode appends one, which this strips).
func marshalNoHTMLEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	return restoreJSONStringifySeparators(raw), nil
}

var (
	jsonEscapeLineSeparator      = []byte(`\u2028`)
	jsonEscapeParagraphSeparator = []byte(`\u2029`)
	jsonLineSeparatorUTF8        = []byte{0xe2, 0x80, 0xa8}
	jsonParagraphSeparatorUTF8   = []byte{0xe2, 0x80, 0xa9}
)

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

func marshalJSONLine(value any) ([]byte, error) {
	raw, err := marshalNoHTMLEscape(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
