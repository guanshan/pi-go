package tools

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizeBashOutputStripsAnsiBinaryAndCR(t *testing.T) {
	in := "\x1b[31mRED\x1b[0m\x00\rTAIL\r\n"
	got := SanitizeBashOutput(in)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("ANSI not stripped: %q", got)
	}
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("NUL not stripped: %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("CR not stripped: %q", got)
	}
	// Newlines are preserved; visible text survives.
	if got != "REDTAIL\n" {
		t.Fatalf("unexpected sanitized output: %q", got)
	}
}

func TestSanitizeAndTruncateBashOutputBelowLimit(t *testing.T) {
	res := SanitizeAndTruncateBashOutput("hello\x1b[0m world")
	if res.Truncated {
		t.Fatalf("small output should not truncate: %#v", res)
	}
	if res.FullOutputPath != "" {
		t.Fatalf("no temp file expected when not truncated, got %q", res.FullOutputPath)
	}
	if res.Output != "hello world" {
		t.Fatalf("sanitized output mismatch: %q", res.Output)
	}
}

func TestSanitizeAndTruncateBashOutputAboveLimitSpillsFile(t *testing.T) {
	// Build > DefaultMaxBytes of line-oriented output.
	var b strings.Builder
	line := strings.Repeat("x", 79) + "\n" // 80 bytes/line
	for b.Len() <= DefaultMaxBytes+2000 {
		b.WriteString(line)
	}
	full := b.String()
	res := SanitizeAndTruncateBashOutput(full)
	if !res.Truncated {
		t.Fatalf("expected truncation for %d bytes", len(full))
	}
	if res.FullOutputPath == "" {
		t.Fatal("expected FullOutputPath on truncation")
	}
	if len(res.Output) >= len(full) {
		t.Fatalf("truncated output (%d) should be smaller than full (%d)", len(res.Output), len(full))
	}
	onDisk, err := os.ReadFile(res.FullOutputPath)
	if err != nil {
		t.Fatalf("read spilled file: %v", err)
	}
	if string(onDisk) != full {
		t.Fatalf("spilled file should contain the full sanitized output (%d vs %d)", len(onDisk), len(full))
	}
}

// TestSanitizeBinaryOutputPreservesRealReplacementChar locks the byte-width-aware
// behavior of the bash executor's sanitizer (the path ExecuteBash actually uses):
// a legitimate U+FFFD (valid EF BF BD, decoded with size 3) survives, while a
// genuinely invalid byte (decoded as utf8.RuneError with size 1) is dropped.
// Mirrors packages/coding-agent/utils.go and packages/agent/harness/utils
// SanitizeShellBinaryOutput. Before the fix, the rune-range loop collapsed both
// cases to utf8.RuneError and swallowed legit replacement characters.
func TestSanitizeBinaryOutputPreservesRealReplacementChar(t *testing.T) {
	if got := SanitizeBinaryOutput("a�b"); got != "a�b" {
		t.Fatalf("real U+FFFD dropped: %q", got)
	}
	if got := SanitizeBinaryOutput("a\xffb"); got != "ab" {
		t.Fatalf("invalid byte not dropped: %q", got)
	}
	// A real U+FFFD must also survive the full bash transform (strip ANSI + CR).
	if got := SanitizeBashOutput("x�\ry"); got != "x�y" {
		t.Fatalf("U+FFFD lost through bash transform: %q", got)
	}
}
