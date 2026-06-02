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
