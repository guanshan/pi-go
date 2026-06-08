// markdown_highlight.go — optional chroma-based syntax highlighting for fenced
// code blocks. Activated via MarkdownTheme.SyntaxHighlight; otherwise unused.
//
// The TS upstream uses highlight.js; here we use alecthomas/chroma to tokenize
// the code body and emit ANSI-colored lines that the markdown renderer drops
// inside the existing ``` border lines.

package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// defaultSyntaxStyle is used when MarkdownTheme.SyntaxStyle is empty.
const defaultSyntaxStyle = "github-dark"

// highlightCodeBlock tokenizes code with the lexer for lang and renders it into
// ANSI-colored lines using a terminal truecolor formatter. It returns the
// colorized lines (one per source line, with the trailing chroma-added empty
// line trimmed) and true on success. On any failure — empty/unknown lang, nil
// lexer/style/formatter, or a tokenise/format error — it returns (nil, false)
// so the caller can fall back to its uniform styling.
//
// The caller remains responsible for the per-line CodeBlockIndent prefix and
// for the surrounding ``` border lines; this helper only colorizes the body.
func highlightCodeBlock(code, lang, styleName string) ([]string, bool) {
	if strings.TrimSpace(lang) == "" {
		return nil, false
	}
	// An empty body has zero lines in the uniform path; chroma would format ""
	// into a single blank line, so fall back to keep the line count consistent.
	if code == "" {
		return nil, false
	}

	// Resolve a lexer: explicit by name, then content analysis, then fallback.
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	if lexer == nil {
		return nil, false
	}

	// Resolve a style; styles.Get already falls back, but guard nil defensively.
	if strings.TrimSpace(styleName) == "" {
		styleName = defaultSyntaxStyle
	}
	style := styles.Get(styleName)
	if style == nil {
		style = styles.Fallback
	}
	if style == nil {
		return nil, false
	}

	// Truecolor terminal formatter to match our truecolor theme styles.
	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		return nil, false
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return nil, false
	}

	var buf strings.Builder
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return nil, false
	}

	out := buf.String()
	// Drop a single trailing newline that chroma commonly appends so the line
	// count matches the source body.
	out = strings.TrimSuffix(out, "\n")
	lines := strings.Split(out, "\n")

	// Normalize to the input line count: trimming the trailing newline above
	// removes a single empty terminal line, but guard against any residual
	// off-by-one so callers can rely on line-count preservation.
	wantLines := strings.Split(strings.TrimSuffix(code, "\n"), "\n")
	for len(lines) > len(wantLines) && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines, true
}
