package core

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// collectJSONLLines drives the PRODUCTION readJSONLLines framing from RunRPC
// (rpc.go) and collects the delivered lines, so these tests exercise the real
// RPC reader rather than a hand-copied replica (a divergence in the production
// reader now fails the test). It is the Go analogue of attachJsonlLineReader from
// ../pi/packages/coding-agent/src/modes/rpc/jsonl.ts.
func collectJSONLLines(in io.Reader) []string {
	var lines []string
	_ = readJSONLLines(in, func(line string) { lines = append(lines, line) })
	return lines
}

// TestRPCJSONLFramingPreservesSeparatorsAndCRLF ports
// ../pi/packages/coding-agent/test/rpc-jsonl.test.ts (~lines 5-65). It covers
// the RPC JSONL write side (writeRPCJSONLine / marshalJSONStringifyLine) and
// the bufio.Reader.ReadString('\n') framing logic in RunRPC (rpc.go):
//
//  1. U+2028 / U+2029 are NOT escaped on the write side and survive a round trip.
//  2. CRLF-terminated input splits correctly with the trailing '\r' stripped.
//  3. A final line WITHOUT a trailing LF is still delivered.
func TestRPCJSONLFramingPreservesSeparatorsAndCRLF(t *testing.T) {
	// "a<U+2028>b<U+2029>c": Go interprets  /  in an interpreted
	// string literal as the real LINE/PARAGRAPH SEPARATOR code points, matching
	// the TS fixture's "a b c".
	const separatorText = "a b c"

	// (1) serializeJsonLine equivalent: U+2028/U+2029 stay literal, the line
	// ends with '\n', and the payload round-trips through JSON parsing.
	// Mirrors TS: serializeJsonLine({ text: "a b c" }).
	t.Run("serializes without escaping unicode separators", func(t *testing.T) {
		raw, err := marshalJSONStringifyLine(map[string]any{"text": separatorText})
		if err != nil {
			t.Fatalf("marshalJSONStringifyLine error: %v", err)
		}
		line := string(raw)

		// The literal separators must be present in the wire bytes.
		if !strings.Contains(line, separatorText) {
			t.Fatalf("expected literal U+2028/U+2029 in output, got %q", line)
		}
		// Go's encoding/json escapes them as the 6-char ASCII sequences
		// the ASCII text \u2028 / \u2029 (double-quoted with an escaped backslash);
		// assert those ASCII escapes are NOT present, matching JSON.stringify
		// and the TS expectation.
		if strings.Contains(line, "\\u2028") || strings.Contains(line, "\\u2029") {
			t.Fatalf("U+2028/U+2029 must not be escaped, got %q", line)
		}
		if !strings.HasSuffix(line, "\n") {
			t.Fatalf("expected trailing LF, got %q", line)
		}

		var decoded map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &decoded); err != nil {
			t.Fatalf("JSON.parse equivalent failed: %v (line=%q)", err, line)
		}
		if decoded["text"] != separatorText {
			t.Fatalf("round-trip mismatch: got %q", decoded["text"])
		}
	})

	// Splits on LF only and preserves U+2028/U+2029 inside payloads.
	// Mirrors TS: Readable.from([serializeJsonLine({ text: "a b c" })]).
	t.Run("splits on LF only and preserves separators", func(t *testing.T) {
		raw, err := marshalJSONStringifyLine(map[string]any{"text": separatorText})
		if err != nil {
			t.Fatalf("marshalJSONStringifyLine error: %v", err)
		}
		lines := collectJSONLLines(strings.NewReader(string(raw)))
		if len(lines) != 1 {
			t.Fatalf("expected 1 line, got %d: %#v", len(lines), lines)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
			t.Fatalf("JSON.parse equivalent failed: %v (line=%q)", err, lines[0])
		}
		if decoded["text"] != separatorText {
			t.Fatalf("U+2028/U+2029 not preserved through reader: got %q", decoded["text"])
		}
	})

	// (2) CRLF-delimited input splits correctly; trailing '\r' is stripped.
	// Mirrors TS: Buffer.from('{"a":1}\r\n{"b":2}\r\n') -> ['{"a":1}', '{"b":2}'].
	t.Run("handles CRLF-delimited input", func(t *testing.T) {
		lines := collectJSONLLines(strings.NewReader("{\"a\":1}\r\n{\"b\":2}\r\n"))
		want := []string{`{"a":1}`, `{"b":2}`}
		if len(lines) != len(want) {
			t.Fatalf("expected %d lines, got %d: %#v", len(want), len(lines), lines)
		}
		for i, w := range want {
			if lines[i] != w {
				t.Fatalf("line[%d]=%q, want %q (carriage return not stripped?)", i, lines[i], w)
			}
		}
	})

	// (3) A final line WITHOUT a trailing LF is still delivered.
	// Mirrors TS: Buffer.from('{"a":1}') -> ['{"a":1}'].
	t.Run("emits final line without trailing LF", func(t *testing.T) {
		lines := collectJSONLLines(strings.NewReader(`{"a":1}`))
		if len(lines) != 1 || lines[0] != `{"a":1}` {
			t.Fatalf("expected final un-terminated line to be delivered, got %#v", lines)
		}
	})
}
