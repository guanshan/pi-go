// markdown.go — ANSI markdown rendering driven by goldmark.
//
// Replaces the previous hand-rolled GFM-ish parser. Block / inline parsing
// is delegated to goldmark (with GFM extensions for tables, strikethrough,
// task lists, autolink); this file only defines the AST walker that emits
// ANSI-styled lines.

package tui

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Markdown renders GFM markdown into ANSI-styled lines.
type Markdown struct {
	Text             string
	PaddingX         int
	PaddingY         int
	Theme            MarkdownTheme
	DefaultTextStyle *DefaultTextStyle
	Options          MarkdownOptions
}

// MarkdownStyleFunc styles a string fragment.
type MarkdownStyleFunc func(string) string

// DefaultTextStyle adds package-wide styling on top of the theme.
type DefaultTextStyle struct {
	Color         MarkdownStyleFunc
	BgColor       MarkdownStyleFunc
	Bold          bool
	Italic        bool
	Strikethrough bool
	Underline     bool
}

// MarkdownTheme selects the styling functions for each markdown construct.
type MarkdownTheme struct {
	Heading         MarkdownStyleFunc
	Link            MarkdownStyleFunc
	LinkURL         MarkdownStyleFunc
	Code            MarkdownStyleFunc
	CodeBlock       MarkdownStyleFunc
	CodeBlockBorder MarkdownStyleFunc
	Quote           MarkdownStyleFunc
	QuoteBorder     MarkdownStyleFunc
	HR              MarkdownStyleFunc
	ListBullet      MarkdownStyleFunc
	Bold            MarkdownStyleFunc
	Italic          MarkdownStyleFunc
	Strikethrough   MarkdownStyleFunc
	Underline       MarkdownStyleFunc
	CodeBlockIndent string
	// SyntaxHighlight, when true, tokenizes + colorizes fenced code blocks via
	// chroma instead of applying the uniform CodeBlock style.
	SyntaxHighlight bool
	// SyntaxStyle is the chroma style name (e.g. "github-dark"); empty selects a
	// sensible default.
	SyntaxStyle string
}

// MarkdownOptions tweaks rendering specifics.
type MarkdownOptions struct {
	PreserveOrderedListMarkers bool
}

// NewMarkdown constructs a Markdown component.
func NewMarkdown(text string, paddingX, paddingY int, theme MarkdownTheme) *Markdown {
	return &Markdown{Text: text, PaddingX: paddingX, PaddingY: paddingY, Theme: theme}
}

// markdownGM is a process-shared parser. goldmark documents are stateless,
// so reuse is safe.
var markdownGM = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM, // table + strikethrough + linkify + tasklist
	),
)

// Render produces the rendered, padded, optionally bg-styled lines.
func (m *Markdown) Render(width int) []string {
	inner := width - m.PaddingX*2
	if inner < 1 {
		inner = 1
	}
	if strings.TrimSpace(m.Text) == "" {
		return nil
	}
	theme := m.markdownTheme()
	source := []byte(strings.ReplaceAll(m.Text, "\t", "   "))
	doc := markdownGM.Parser().Parse(text.NewReader(source))

	r := &mdRenderer{
		md:     m,
		theme:  theme,
		width:  inner,
		source: source,
	}
	rendered := r.renderNode(doc, 0)

	leftPad := strings.Repeat(" ", m.PaddingX)
	rightPad := strings.Repeat(" ", m.PaddingX)
	var lines []string
	for i := 0; i < m.PaddingY; i++ {
		lines = append(lines, m.decorateMarkdownLine(strings.Repeat(" ", width), width))
	}
	for _, line := range rendered {
		if IsImageLine(line) {
			lines = append(lines, line)
			continue
		}
		lines = append(lines, m.decorateMarkdownLine(leftPad+line+rightPad, width))
	}
	for i := 0; i < m.PaddingY; i++ {
		lines = append(lines, m.decorateMarkdownLine(strings.Repeat(" ", width), width))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m *Markdown) markdownTheme() MarkdownTheme {
	theme := m.Theme
	if theme.Heading == nil {
		theme.Heading = func(s string) string { return "\x1b[36m" + s + "\x1b[0m" }
	}
	if theme.Link == nil {
		theme.Link = func(s string) string { return "\x1b[34m" + s + "\x1b[0m" }
	}
	if theme.LinkURL == nil {
		theme.LinkURL = func(s string) string { return "\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.Code == nil {
		theme.Code = func(s string) string { return "\x1b[33m" + s + "\x1b[0m" }
	}
	if theme.CodeBlock == nil {
		theme.CodeBlock = theme.Code
	}
	if theme.CodeBlockBorder == nil {
		theme.CodeBlockBorder = func(s string) string { return "\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.Quote == nil {
		theme.Quote = func(s string) string { return "\x1b[3m\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.QuoteBorder == nil {
		theme.QuoteBorder = func(s string) string { return "\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.HR == nil {
		theme.HR = func(s string) string { return "\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.ListBullet == nil {
		theme.ListBullet = func(s string) string { return "\x1b[90m" + s + "\x1b[0m" }
	}
	if theme.Bold == nil {
		theme.Bold = func(s string) string { return "\x1b[1m" + s + "\x1b[0m" }
	}
	if theme.Italic == nil {
		theme.Italic = func(s string) string { return "\x1b[3m" + s + "\x1b[0m" }
	}
	if theme.Strikethrough == nil {
		theme.Strikethrough = func(s string) string { return "\x1b[9m" + s + "\x1b[0m" }
	}
	if theme.Underline == nil {
		theme.Underline = func(s string) string { return "\x1b[4m" + s + "\x1b[0m" }
	}
	if theme.CodeBlockIndent == "" {
		theme.CodeBlockIndent = "  "
	}
	return theme
}

func (m *Markdown) decorateMarkdownLine(line string, width int) string {
	padding := width - VisibleWidth(line)
	if padding > 0 {
		line += strings.Repeat(" ", padding)
	}
	if m.DefaultTextStyle != nil && m.DefaultTextStyle.BgColor != nil {
		return m.DefaultTextStyle.BgColor(line)
	}
	return line
}

func (m *Markdown) applyDefaultStyle(text string, theme MarkdownTheme) string {
	if m.DefaultTextStyle == nil {
		return text
	}
	styled := text
	if m.DefaultTextStyle.Color != nil {
		styled = m.DefaultTextStyle.Color(styled)
	}
	if m.DefaultTextStyle.Bold {
		styled = theme.Bold(styled)
	}
	if m.DefaultTextStyle.Italic {
		styled = theme.Italic(styled)
	}
	if m.DefaultTextStyle.Strikethrough {
		styled = theme.Strikethrough(styled)
	}
	if m.DefaultTextStyle.Underline {
		styled = theme.Underline(styled)
	}
	return styled
}

// =============================================================================
// AST walker
// =============================================================================

type mdRenderer struct {
	md     *Markdown
	theme  MarkdownTheme
	width  int
	source []byte
}

// renderNode renders one block-level node + its successors at the given
// width budget, returning the resulting lines.
func (r *mdRenderer) renderNode(n ast.Node, indent int) []string {
	var out []string
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		var lines []string
		switch v := child.(type) {
		case *ast.Heading:
			lines = r.renderHeading(v)
		case *ast.Paragraph:
			lines = r.renderParagraph(v, r.width-indent)
		case *ast.TextBlock:
			lines = r.renderParagraph(v, r.width-indent)
		case *ast.FencedCodeBlock:
			lines = r.renderFencedCode(v)
		case *ast.CodeBlock:
			lines = r.renderIndentedCode(v)
		case *ast.Blockquote:
			lines = r.renderBlockquote(v)
		case *ast.List:
			lines = r.renderList(v, indent)
		case *ast.ListItem:
			lines = r.renderListItem(v, "- ", indent)
		case *ast.ThematicBreak:
			lines = r.renderHR()
		case *ast.HTMLBlock:
			lines = r.renderHTMLBlock(v)
		case *extast.Table:
			lines = r.renderTable(v)
		default:
			lines = r.renderParagraphFallback(v, r.width-indent)
		}
		if len(lines) > 0 {
			// Parity note (accepted divergence): TS markdown.ts conditions the
			// inter-block blank line on the source's blank-line structure — it
			// renders an explicit marked "space" token per source blank line and
			// suppresses a block's own trailing blank when the next token is a
			// "space" (or, for paragraphs, a "list"). goldmark's AST has no
			// blank-line/space node, so the source blank-line count is
			// unrecoverable here. We therefore insert exactly one blank line
			// between adjacent non-empty block renders. This matches TS for the
			// common case (single blank line between blocks, e.g. a paragraph
			// followed by a blank line then a list) but cannot reproduce TS for
			// 2+ consecutive source blanks or tightly-packed blocks with no blank
			// line. Inherent to the goldmark-vs-marked parser swap; see
			// PARITY_AUDIT_2026-06-08.md P3-33.
			if len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, lines...)
		}
	}
	return out
}

func (r *mdRenderer) renderHeading(h *ast.Heading) []string {
	inline := r.renderInline(h, r.theme)
	if h.Level == 1 {
		styled := r.theme.Heading(r.theme.Bold(r.theme.Underline(inline)))
		return wrapAndStyle(styled, r.width, r.md, r.theme)
	}
	body := inline
	if h.Level >= 3 {
		body = strings.Repeat("#", h.Level) + " " + inline
	}
	styled := r.theme.Heading(r.theme.Bold(body))
	return wrapAndStyle(styled, r.width, r.md, r.theme)
}

func (r *mdRenderer) renderParagraph(p ast.Node, width int) []string {
	if width < 1 {
		width = 1
	}
	inline := r.renderInline(p, r.theme)
	if IsImageLine(inline) {
		return []string{inline}
	}
	return wrapAndStyle(inline, width, r.md, r.theme)
}

func (r *mdRenderer) renderParagraphFallback(n ast.Node, width int) []string {
	if width < 1 {
		width = 1
	}
	text := r.renderInline(n, r.theme)
	if text == "" {
		return nil
	}
	return wrapAndStyle(text, width, r.md, r.theme)
}

func (r *mdRenderer) renderFencedCode(f *ast.FencedCodeBlock) []string {
	lang := string(f.Language(r.source))
	lines := []string{r.theme.CodeBlockBorder("```" + lang)}

	if r.theme.SyntaxHighlight {
		var body strings.Builder
		for i := 0; i < f.Lines().Len(); i++ {
			seg := f.Lines().At(i)
			body.Write(seg.Value(r.source))
		}
		if hl, ok := highlightCodeBlock(body.String(), lang, r.theme.SyntaxStyle); ok {
			for _, line := range hl {
				// highlighted lines are already styled — do not re-wrap in CodeBlock.
				lines = append(lines, r.theme.CodeBlockIndent+line)
			}
			lines = append(lines, r.theme.CodeBlockBorder("```"))
			return lines
		}
	}

	for i := 0; i < f.Lines().Len(); i++ {
		seg := f.Lines().At(i)
		// strip trailing newline only (preserve interior whitespace)
		body := strings.TrimRight(string(seg.Value(r.source)), "\n")
		lines = append(lines, r.theme.CodeBlockIndent+r.theme.CodeBlock(body))
	}
	lines = append(lines, r.theme.CodeBlockBorder("```"))
	return lines
}

func (r *mdRenderer) renderIndentedCode(c *ast.CodeBlock) []string {
	var lines []string
	for i := 0; i < c.Lines().Len(); i++ {
		seg := c.Lines().At(i)
		body := strings.TrimRight(string(seg.Value(r.source)), "\n")
		lines = append(lines, r.theme.CodeBlockIndent+r.theme.CodeBlock(body))
	}
	return lines
}

func (r *mdRenderer) renderBlockquote(q *ast.Blockquote) []string {
	innerWidth := maxInt(1, r.width-2)
	innerR := &mdRenderer{md: r.md, theme: r.theme, width: innerWidth, source: r.source}
	inner := innerR.renderNode(q, 0)
	var out []string
	for _, line := range inner {
		if line == "" {
			out = append(out, r.theme.QuoteBorder("| "))
			continue
		}
		styled := r.theme.Quote(line)
		out = append(out, r.theme.QuoteBorder("| ")+styled)
	}
	return out
}

func (r *mdRenderer) renderList(list *ast.List, indent int) []string {
	var out []string
	itemIndex := 0
	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		item, ok := child.(*ast.ListItem)
		if !ok {
			continue
		}
		var bullet string
		if list.IsOrdered() {
			if r.md.Options.PreserveOrderedListMarkers {
				bullet = fmt.Sprintf("%d. ", list.Start+itemIndex)
			} else {
				bullet = "- "
			}
		} else {
			bullet = "- "
		}
		lines := r.renderListItem(item, bullet, indent)
		out = append(out, lines...)
		itemIndex++
	}
	return out
}

func (r *mdRenderer) renderListItem(item *ast.ListItem, bullet string, indent int) []string {
	bulletStyled := r.theme.ListBullet(bullet)
	bulletWidth := VisibleWidth(bullet)
	innerWidth := maxInt(1, r.width-indent-bulletWidth)
	contLeftPad := strings.Repeat(" ", bulletWidth)

	var out []string
	first := true
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		inner := &mdRenderer{md: r.md, theme: r.theme, width: innerWidth, source: r.source}
		var lines []string
		switch v := child.(type) {
		case *ast.TextBlock, *ast.Paragraph:
			lines = inner.renderParagraph(v, innerWidth)
		case *ast.List:
			lines = inner.renderList(v, 0)
		default:
			lines = inner.renderParagraphFallback(v, innerWidth)
		}
		for i, line := range lines {
			if first && i == 0 {
				out = append(out, bulletStyled+line)
				first = false
				continue
			}
			out = append(out, contLeftPad+line)
		}
	}
	return out
}

func (r *mdRenderer) renderHR() []string {
	n := r.width
	if n > 80 {
		n = 80
	}
	if n < 1 {
		n = 1
	}
	return []string{r.theme.HR(strings.Repeat("-", n))}
}

func (r *mdRenderer) renderHTMLBlock(h *ast.HTMLBlock) []string {
	var out []string
	for i := 0; i < h.Lines().Len(); i++ {
		seg := h.Lines().At(i)
		out = append(out, strings.TrimRight(string(seg.Value(r.source)), "\n"))
	}
	return out
}

func (r *mdRenderer) renderTable(t *extast.Table) []string {
	rows := [][]string{}
	colCount := 0
	for child := t.FirstChild(); child != nil; child = child.NextSibling() {
		switch row := child.(type) {
		case *extast.TableHeader:
			cells := r.collectTableCells(row)
			if len(cells) > colCount {
				colCount = len(cells)
			}
			rows = append(rows, cells)
		case *extast.TableRow:
			cells := r.collectTableCells(row)
			if len(cells) > colCount {
				colCount = len(cells)
			}
			rows = append(rows, cells)
		}
	}
	if colCount == 0 {
		return nil
	}
	widths := make([]int, colCount)
	for _, row := range rows {
		for c := 0; c < colCount && c < len(row); c++ {
			if w := VisibleWidth(row[c]); w > widths[c] {
				widths[c] = w
			}
		}
	}
	var out []string
	for ri, row := range rows {
		var parts []string
		for c := 0; c < colCount; c++ {
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			parts = append(parts, cell+strings.Repeat(" ", maxInt(0, widths[c]-VisibleWidth(cell))))
		}
		out = append(out, TruncateToWidth(strings.Join(parts, "  "), r.width, "..."))
		if ri == 0 {
			var sep []string
			for c := 0; c < colCount; c++ {
				sep = append(sep, strings.Repeat("-", maxInt(3, widths[c])))
			}
			out = append(out, r.theme.CodeBlockBorder(strings.Join(sep, "  ")))
		}
	}
	return out
}

func (r *mdRenderer) collectTableCells(row ast.Node) []string {
	var out []string
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		out = append(out, r.renderInline(cell, r.theme))
	}
	return out
}

// =============================================================================
// Inline rendering
// =============================================================================

func (r *mdRenderer) renderInline(n ast.Node, theme MarkdownTheme) string {
	var b bytes.Buffer
	r.walkInline(&b, n, theme)
	return r.md.applyDefaultStyle(b.String(), theme)
}

func (r *mdRenderer) walkInline(b *bytes.Buffer, n ast.Node, theme MarkdownTheme) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			seg := v.Segment
			b.Write(seg.Value(r.source))
			if v.HardLineBreak() {
				b.WriteByte('\n')
			} else if v.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(v.Value)
		case *ast.CodeSpan:
			var inner bytes.Buffer
			r.walkInline(&inner, v, theme)
			b.WriteString(theme.Code(inner.String()))
		case *ast.Emphasis:
			var inner bytes.Buffer
			r.walkInline(&inner, v, theme)
			if v.Level == 2 {
				b.WriteString(theme.Bold(inner.String()))
			} else {
				b.WriteString(theme.Italic(inner.String()))
			}
		case *extast.Strikethrough:
			var inner bytes.Buffer
			r.walkInline(&inner, v, theme)
			b.WriteString(theme.Strikethrough(inner.String()))
		case *ast.Link:
			var inner bytes.Buffer
			r.walkInline(&inner, v, theme)
			label := inner.String()
			href := string(v.Destination)
			styled := theme.Link(theme.Underline(label))
			if GetCapabilities().Hyperlinks {
				b.WriteString(Hyperlink(styled, href))
			} else {
				compare := strings.TrimPrefix(href, "mailto:")
				if label == href || label == compare {
					b.WriteString(styled)
				} else {
					b.WriteString(styled)
					b.WriteString(" ")
					b.WriteString(theme.LinkURL("(" + href + ")"))
				}
			}
		case *ast.AutoLink:
			href := string(v.URL(r.source))
			styled := theme.Link(theme.Underline(href))
			if GetCapabilities().Hyperlinks {
				b.WriteString(Hyperlink(styled, href))
			} else {
				b.WriteString(styled)
			}
		case *ast.Image:
			var inner bytes.Buffer
			r.walkInline(&inner, v, theme)
			alt := inner.String()
			href := string(v.Destination)
			out := theme.Link(theme.Underline("![" + alt + "]"))
			if GetCapabilities().Hyperlinks {
				b.WriteString(Hyperlink(out, href))
			} else {
				b.WriteString(out + " " + theme.LinkURL("("+href+")"))
			}
		case *ast.RawHTML:
			for i := 0; i < v.Segments.Len(); i++ {
				seg := v.Segments.At(i)
				b.Write(seg.Value(r.source))
			}
		default:
			r.walkInline(b, v, theme)
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

// wrapAndStyle wraps text to width and threads the markdown's default style
// through each resulting line.
func wrapAndStyle(s string, width int, m *Markdown, theme MarkdownTheme) []string {
	if width < 1 {
		width = 1
	}
	wrapped := WrapTextWithANSI(s, width)
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}
