package tui

import "strings"

type Text struct {
	Text     string
	PaddingX int
	PaddingY int
}

func NewText(text string, paddingX, paddingY int) *Text {
	return &Text{Text: text, PaddingX: paddingX, PaddingY: paddingY}
}

func (t *Text) SetText(text string) {
	t.Text = text
}

func (t *Text) Render(width int) []string {
	inner := width - t.PaddingX*2
	if inner < 1 {
		inner = 1
	}
	var lines []string
	for i := 0; i < t.PaddingY; i++ {
		lines = append(lines, "")
	}
	for _, paragraph := range strings.Split(t.Text, "\n") {
		for _, line := range WrapTextWithANSI(paragraph, inner) {
			lines = append(lines, strings.Repeat(" ", t.PaddingX)+TruncateToWidth(line, inner, ""))
		}
	}
	for i := 0; i < t.PaddingY; i++ {
		lines = append(lines, "")
	}
	return lines
}
