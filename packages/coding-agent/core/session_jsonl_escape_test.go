package core

import (
	"bytes"
	"strings"
	"testing"
)

// TestWriteJSONLineNoHTMLEscape asserts that session JSONL serialization does
// not HTML-escape `<`, `>`, `&` (matching TS JSON.stringify) and preserves
// emoji and U+2028/U+2029 verbatim, just like writeRPCJSONLine on the RPC path.
func TestWriteJSONLineNoHTMLEscape(t *testing.T) {
	seps := "line" + string(rune(0x2028)) + "para" + string(rune(0x2029)) + "end"
	payload := map[string]string{
		"code":    "if (a < b && c > d) { x(); }",
		"amp":     "Tom & Jerry",
		"emoji":   "rocket \U0001F680 ok",
		"literal": `literal \u2028 text`,
		"seps":    seps,
	}
	var buf bytes.Buffer
	if err := writeJSONLine(&buf, payload); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}
	got := buf.String()

	// `<`, `>`, `&`, emoji, and the JS line/paragraph separators are left literal
	// (matches JSON.stringify exactly).
	for _, want := range []string{
		`"if (a < b && c > d) { x(); }"`,
		`"Tom & Jerry"`,
		"rocket \U0001F680 ok",
		seps,
		`"literal":"literal \\u2028 text"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output: %q", want, got)
		}
	}
	for _, escaped := range []string{"\\u003c", "\\u003e", "\\u0026", "\\u2029"} {
		if strings.Contains(got, escaped) {
			t.Errorf("output unexpectedly escaped (%s)\nfull output: %q", escaped, got)
		}
	}
	if strings.Contains(got, `"seps":"line\u2028`) {
		t.Errorf("real U+2028 was escaped instead of left literal\nfull output: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output not newline-terminated: %q", got)
	}
}
