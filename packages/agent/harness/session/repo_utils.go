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

// marshalNoHTMLEscape serializes value to JSON without escaping <, >, and &.
//
// Go's stdlib json.Marshal HTML-escapes those runes (e.g. < -> <), but TS
// JSON.stringify (used by jsonl-storage.ts to write session files) never does.
// Session content routinely contains <, >, and & (HTML tags, &&, List<String>,
// a < b), so the default escaping makes Go-written session files differ
// byte-for-byte from TS-written ones. We disable HTML escaping to match TS.
//
// Note: json.Encoder always escapes U+2028/U+2029 to  /  even with
// SetEscapeHTML(false) (a hard-coded encoder behavior with no public off switch),
// whereas TS leaves them literal. That residual divergence is harmless (it decodes
// back to the same character) and is the only byte-level difference that remains.
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
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func marshalJSONLine(value any) ([]byte, error) {
	raw, err := marshalNoHTMLEscape(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
