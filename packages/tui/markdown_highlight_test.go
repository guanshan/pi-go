package tui

import (
	"regexp"
	"strings"
	"testing"
)

// sgrRe matches an ANSI SGR (color/style) escape sequence.
var sgrRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestMarkdownSyntaxHighlightDefaultOffIdentical(t *testing.T) {
	src := "```go\nx := 1\ny := 2\n```"

	// Zero-value theme: SyntaxHighlight defaults to false.
	off := (&Markdown{Text: src}).Render(60)
	// Explicitly false must be byte-identical to the zero-value default.
	explicit := (&Markdown{Text: src, Theme: MarkdownTheme{SyntaxHighlight: false}}).Render(60)

	if strings.Join(off, "\n") != strings.Join(explicit, "\n") {
		t.Fatalf("explicit SyntaxHighlight=false differs from default:\n%q\nvs\n%q", off, explicit)
	}

	// The uniform-styled body must not contain SGR color codes beyond the single
	// CodeBlock wrapper the renderer already applies — i.e. each body line has
	// exactly one styling pair, not chroma's multi-color output.
	joined := strings.Join(off, "\n")
	if !strings.Contains(joined, "x := 1") || !strings.Contains(joined, "y := 2") {
		t.Fatalf("default code body missing content: %q", joined)
	}
}

func TestMarkdownSyntaxHighlightGoColorized(t *testing.T) {
	src := "```go\npackage main\n\nfunc main() {\n\tx := 1\n}\n```"

	on := (&Markdown{
		Text:  src,
		Theme: MarkdownTheme{SyntaxHighlight: true, SyntaxStyle: "github-dark"},
	}).Render(80)
	joined := strings.Join(on, "\n")

	// Must contain ANSI SGR escapes (got colorized).
	if !strings.Contains(joined, "\x1b[") {
		t.Fatalf("highlighted output has no ANSI escapes: %q", joined)
	}

	// Must be multi-color: more than one distinct SGR code present.
	distinct := map[string]struct{}{}
	for _, m := range sgrRe.FindAllString(joined, -1) {
		distinct[m] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf("expected multiple distinct colors, got %d: %v", len(distinct), distinct)
	}

	// Line count of the code body must be preserved. Compare against the
	// uniform (highlight-off) render's body line count for the same source.
	off := (&Markdown{Text: src}).Render(80)
	if bodyLineCount(on) != bodyLineCount(off) {
		t.Fatalf("body line count mismatch: highlighted=%d uniform=%d\non=%q\noff=%q",
			bodyLineCount(on), bodyLineCount(off), on, off)
	}
}

func TestMarkdownSyntaxHighlightUnknownLangGraceful(t *testing.T) {
	src := "```not-a-real-language-xyz\nsome content here\nsecond line\n```"

	// Must not panic; lines must be preserved.
	on := (&Markdown{
		Text:  src,
		Theme: MarkdownTheme{SyntaxHighlight: true},
	}).Render(80)
	joined := strings.Join(on, "\n")

	if !strings.Contains(joined, "some content here") || !strings.Contains(joined, "second line") {
		t.Fatalf("unknown-lang fallback lost content: %q", joined)
	}

	off := (&Markdown{Text: src}).Render(80)
	if bodyLineCount(on) != bodyLineCount(off) {
		t.Fatalf("unknown-lang body line count mismatch: on=%d off=%d", bodyLineCount(on), bodyLineCount(off))
	}
}

func TestHighlightCodeBlockDirect(t *testing.T) {
	code := "package main\n\nfunc main() {}\n"

	lines, ok := highlightCodeBlock(code, "go", "github-dark")
	if !ok {
		t.Fatalf("expected highlightCodeBlock ok for go")
	}
	// Trailing newline trimmed => 3 source lines preserved.
	want := strings.Split(strings.TrimSuffix(code, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("line count: got %d want %d (%q)", len(lines), len(want), lines)
	}

	// Empty lang => not ok.
	if _, ok := highlightCodeBlock(code, "", "github-dark"); ok {
		t.Fatalf("expected not ok for empty lang")
	}
	// Empty body => not ok (so an empty fenced block does not gain a spurious
	// blank line vs the uniform 0-line render).
	if got, ok := highlightCodeBlock("", "go", "github-dark"); ok {
		t.Fatalf("expected not ok for empty body, got %d lines", len(got))
	}
}

// bodyLineCount counts rendered lines that sit between the ``` border lines.
func bodyLineCount(rendered []string) int {
	count := 0
	inside := false
	for _, line := range rendered {
		stripped := sgrRe.ReplaceAllString(line, "")
		stripped = strings.TrimSpace(stripped)
		if strings.HasPrefix(stripped, "```") {
			if !inside {
				inside = true
				continue
			}
			break
		}
		if inside {
			count++
		}
	}
	return count
}
