package tui

import "fmt"

type Image struct {
	AltText string
	Width   int
	Height  int
}

func (i *Image) Render(width int) []string {
	label := i.AltText
	if label == "" {
		label = "image"
	}
	return []string{TruncateToWidth(fmt.Sprintf("[Image: %s %dx%d]", label, i.Width, i.Height), width, "...")}
}
