package harnessutils

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeShellBinaryOutputPreservesRealReplacementChar(t *testing.T) {
	// A legitimately-encoded U+FFFD (bytes EF BF BD) is a valid code point and TS
	// (which iterates code points via codePointAt) keeps it. Only genuinely
	// invalid bytes are stripped.
	if got := SanitizeShellBinaryOutput("a�b"); got != "a�b" {
		t.Fatalf("real U+FFFD dropped: got %q", got)
	}

	// Genuinely invalid UTF-8 (a lone continuation byte 0x80) must be stripped,
	// matching TS where such bytes never appear as decoded code points.
	if got := SanitizeShellBinaryOutput("a\x80b"); got != "ab" {
		t.Fatalf("invalid byte not stripped: got %q", got)
	}

	// Mix: real U+FFFD preserved, lone 0xFF stripped, control byte stripped.
	if got := SanitizeShellBinaryOutput("x�\xFF\x01y"); got != "x�y" {
		t.Fatalf("mixed sanitize: got %q", got)
	}

	// Allowed whitespace controls survive.
	if got := SanitizeShellBinaryOutput("a\tb\nc\rd"); got != "a\tb\nc\rd" {
		t.Fatalf("whitespace controls altered: got %q", got)
	}
}

func TestUTF16Len(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"世界", 2},
		{"\U0001F600", 2},
	}
	for _, tc := range cases {
		if got := utf16Len(tc.in); got != tc.want {
			t.Fatalf("utf16Len(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestExecuteShellWithCapturePreservesMultibyteAndRealReplacementChar(t *testing.T) {
	// Multibyte content (CJK + emoji) and a real U+FFFD must round-trip through
	// capture, sanitization, and the UTF-16-measured tail buffer unchanged.
	env := &fakeShellEnv{outputs: []string{"世界", "\U0001F600", "a�b", "tail"}}
	res, err := ExecuteShellWithCapture(context.Background(), env, "cmd", ShellCaptureOptions{})
	if err != nil {
		t.Fatalf("capture error: %v", err)
	}
	for _, want := range []string{"世界", "\U0001F600", "a�b", "tail"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q: got %q", want, res.Output)
		}
	}
}

// TestExecuteShellWithCaptureHandlesMultibyteSplitAcrossChunks documents what
// happens when a multibyte rune is split across two OnStdout chunks *at the
// ExecuteShellWithCapture boundary* (i.e. each delivered string is itself an
// invalid UTF-8 fragment).
//
// Layering note: ExecuteShellWithCapture does NOT do cross-chunk UTF-8
// reassembly. Reassembly of a rune split across raw byte chunks is the
// execution env's job: LocalExecutionEnv's executionStreamWriter buffers a
// trailing partial rune (splitTrailingPartialRune + flush, see
// packages/agent/harness/env/local.go and TestExecutionStreamWriterReassemblesSplitRune)
// so the OnStdout callback never observes a split rune. This mirrors the TS
// stack: the NodeJS env reads stdout with setEncoding("utf8") (a StringDecoder
// that buffers incomplete multibyte sequences), so by the time TS's
// executeShellWithCapture.onChunk runs it has already received whole code
// points. Neither TS executeShellWithCapture nor Go ExecuteShellWithCapture
// reassembles runes itself.
//
// Therefore, if a caller bypasses that env-level decoder and feeds raw split
// fragments straight into OnStdout, each fragment is sanitized independently:
// the lead byte and the continuation bytes each decode as utf8.RuneError with
// size 1 and are dropped by SanitizeShellBinaryOutput, so the rune is lost.
// We assert this ACTUAL behavior rather than a (non-existent) reassembly.
func TestExecuteShellWithCaptureHandlesMultibyteSplitAcrossChunks(t *testing.T) {
	full := "世" // UTF-8 bytes E4 B8 96
	b := []byte(full)
	lead := string(b[:1])  // E4          (incomplete: lone lead byte)
	rest := string(b[1:2]) // B8          (lone continuation)
	end := string(b[2:])   // 96          (lone continuation)

	// A multibyte rune split across OnStdout chunks is dropped, not reassembled,
	// because each fragment is independently invalid UTF-8.
	env := &fakeShellEnv{outputs: []string{"before ", lead, rest, end, " after"}}
	res, err := ExecuteShellWithCapture(context.Background(), env, "cmd", ShellCaptureOptions{})
	if err != nil {
		t.Fatalf("capture error: %v", err)
	}
	if res.Output != "before  after" {
		t.Fatalf("split rune should be dropped at the capture boundary: got %q (% x)",
			res.Output, []byte(res.Output))
	}
	if strings.ContainsRune(res.Output, '世') {
		t.Fatalf("ExecuteShellWithCapture does not reassemble split runes, but got %q", res.Output)
	}
	if strings.ContainsRune(res.Output, '�') {
		t.Fatalf("invalid fragments should be stripped, not surfaced as U+FFFD: %q", res.Output)
	}

	// Control: when the same rune arrives whole in a single chunk, it survives
	// the capture pipeline byte-for-byte. This confirms the loss above is purely
	// a consequence of the split, not of the multibyte content itself.
	whole := &fakeShellEnv{outputs: []string{"before ", full, " after"}}
	wres, err := ExecuteShellWithCapture(context.Background(), whole, "cmd", ShellCaptureOptions{})
	if err != nil {
		t.Fatalf("capture error (whole): %v", err)
	}
	if wres.Output != "before 世 after" {
		t.Fatalf("whole rune should round-trip: got %q", wres.Output)
	}
}
