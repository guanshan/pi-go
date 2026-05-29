package tui

import (
	"strings"
	"testing"
)

func TestMarkdownRichRendering(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	md := &Markdown{Text: strings.Join([]string{
		"# Title",
		"",
		"Hello **bold** and *italic* with `code` and [site](https://example.com).",
		"",
		"- first item",
		"- second item wraps around a narrow width nicely",
		"",
		"> quoted **text**",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"| Name | Value |",
		"| --- | --- |",
		"| a | b |",
		"",
		"---",
	}, "\n")}
	lines := md.Render(48)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"\x1b[4m",             // underline (h1 / link)
		"\x1b[1m",             // bold
		"\x1b[3m",             // italic
		"\x1b[33mcode\x1b[0m", // inline code
		"https://example.com",
		"first item",
		"```go",
		"fmt.Println(\"hi\")",
		"Name",
		"Value",
		"---",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, joined)
		}
	}
	for _, line := range lines {
		if VisibleWidth(line) > 48 {
			t.Fatalf("line too wide (%d): %q", VisibleWidth(line), line)
		}
	}
}

func TestMarkdownHyperlinksAndVisibleWidth(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{Hyperlinks: true})
	lines := (&Markdown{Text: "[docs](https://example.com/docs)"}).Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "\x1b]8;;https://example.com/docs\x1b\\") {
		t.Fatalf("missing OSC 8 hyperlink: %q", joined)
	}
	if VisibleWidth(Hyperlink("docs", "https://example.com/docs")) != len("docs") {
		t.Fatalf("hyperlink visible width mismatch")
	}
	if VisibleWidth(joined) != 80 {
		t.Fatalf("visible width=%d line=%q", VisibleWidth(joined), joined)
	}
}

func TestMarkdownPaddingWidth(t *testing.T) {
	md := &Markdown{
		Text:     "hello",
		PaddingX: 2,
		PaddingY: 1,
	}
	lines := md.Render(20)
	if len(lines) != 3 {
		t.Fatalf("lines=%#v", lines)
	}
	for _, line := range lines {
		if VisibleWidth(line) != 20 {
			t.Errorf("padded line not exactly 20 wide: %q (=%d)", line, VisibleWidth(line))
		}
	}
}

// =============================================================================
// Goldmark coverage — exercise blocks/inlines, attribute mapping.
// =============================================================================

func TestMarkdownHeadings(t *testing.T) {
	cases := []struct {
		text string
		want []string // substrings expected somewhere in joined output
	}{
		{"# H1", []string{"H1", "\x1b[4m" /* underline only at level 1 */}},
		{"## H2", []string{"H2", "\x1b[1m" /* bold */}},
		{"### H3", []string{"H3", "### "}},
		{"#### H4", []string{"H4", "#### "}},
	}
	for _, c := range cases {
		got := strings.Join((&Markdown{Text: c.text}).Render(40), "\n")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("heading %q missing %q in:\n%s", c.text, w, got)
			}
		}
	}
}

func TestMarkdownEmphasis(t *testing.T) {
	out := strings.Join((&Markdown{Text: "**a** *b* ~~c~~ `d`"}).Render(40), "\n")
	for _, want := range []string{"\x1b[1m", "\x1b[3m", "\x1b[9m", "\x1b[33m"} {
		if !strings.Contains(out, want) {
			t.Errorf("emphasis missing %q in %q", want, out)
		}
	}
}

func TestMarkdownOrderedList(t *testing.T) {
	src := strings.Join([]string{"1. a", "2. b", "3. c"}, "\n")
	// default: ordered markers normalized to "- "
	out := strings.Join((&Markdown{Text: src}).Render(20), "\n")
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Errorf("ordered list missing items: %q", out)
	}
	// preserve markers
	out2 := strings.Join((&Markdown{Text: src, Options: MarkdownOptions{PreserveOrderedListMarkers: true}}).Render(20), "\n")
	if !strings.Contains(out2, "1.") || !strings.Contains(out2, "2.") {
		t.Errorf("preserved markers missing in: %q", out2)
	}
}

func TestMarkdownBlockquoteNested(t *testing.T) {
	src := "> outer\n>\n> > inner"
	out := strings.Join((&Markdown{Text: src}).Render(40), "\n")
	if !strings.Contains(out, "outer") || !strings.Contains(out, "inner") {
		t.Errorf("blockquote: %q", out)
	}
	// quote border characters present
	if !strings.Contains(out, "|") {
		t.Errorf("missing quote border: %q", out)
	}
}

func TestMarkdownCodeFence(t *testing.T) {
	src := "```python\nprint('x')\n```"
	out := (&Markdown{Text: src}).Render(40)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "```python") || !strings.Contains(joined, "print('x')") {
		t.Errorf("code block: %q", joined)
	}
}

func TestMarkdownIndentedCodeBlock(t *testing.T) {
	src := "    x = 1\n    y = 2"
	out := strings.Join((&Markdown{Text: src}).Render(40), "\n")
	if !strings.Contains(out, "x = 1") || !strings.Contains(out, "y = 2") {
		t.Errorf("indented code: %q", out)
	}
}

func TestMarkdownHorizontalRule(t *testing.T) {
	for _, src := range []string{"---", "***", "___"} {
		out := strings.Join((&Markdown{Text: src}).Render(20), "\n")
		if !strings.Contains(out, "---") {
			t.Errorf("HR for %q: %q", src, out)
		}
	}
}

func TestMarkdownTableAlignment(t *testing.T) {
	src := strings.Join([]string{
		"| a | b | c |",
		"| :--- | :---: | ---: |",
		"| 1 | 2 | 3 |",
	}, "\n")
	out := strings.Join((&Markdown{Text: src}).Render(40), "\n")
	for _, want := range []string{"a", "b", "c", "1", "2", "3", "---"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q in %q", want, out)
		}
	}
}

func TestMarkdownAutoLink(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	src := "see <https://example.com> for details"
	out := strings.Join((&Markdown{Text: src}).Render(80), "\n")
	if !strings.Contains(out, "https://example.com") {
		t.Errorf("autolink: %q", out)
	}
}

func TestMarkdownEmptyAndWhitespace(t *testing.T) {
	if got := (&Markdown{Text: ""}).Render(40); got != nil {
		t.Errorf("empty markdown: %#v", got)
	}
	if got := (&Markdown{Text: "   \n\n  "}).Render(40); got != nil {
		t.Errorf("whitespace markdown: %#v", got)
	}
}

func TestMarkdownNestedList(t *testing.T) {
	src := strings.Join([]string{
		"- outer",
		"  - inner",
		"  - inner2",
	}, "\n")
	out := strings.Join((&Markdown{Text: src}).Render(40), "\n")
	for _, want := range []string{"outer", "inner", "inner2"} {
		if !strings.Contains(out, want) {
			t.Errorf("nested list missing %q in %q", want, out)
		}
	}
}

func TestMarkdownLinkSurroundingText(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{Hyperlinks: false})
	src := "before [link](https://example.com) after"
	out := strings.Join((&Markdown{Text: src}).Render(80), "\n")
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("surrounding text lost: %q", out)
	}
	if !strings.Contains(out, "https://example.com") {
		t.Errorf("link url lost: %q", out)
	}
}

func TestMarkdownDefaultTextStyle(t *testing.T) {
	red := func(s string) string { return "\x1b[31m" + s + "\x1b[0m" }
	md := &Markdown{
		Text: "hello",
		DefaultTextStyle: &DefaultTextStyle{
			Color: red,
			Bold:  true,
		},
	}
	out := strings.Join(md.Render(20), "\n")
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("color missing: %q", out)
	}
	if !strings.Contains(out, "\x1b[1m") {
		t.Errorf("bold missing: %q", out)
	}
}
