package tui

import (
	"fmt"
	"strings"
)

type Image struct {
	AltText   string
	Width     int
	Height    int
	Data      []byte
	MaxWidth  int
	MaxHeight int
}

func (i *Image) Render(width int) []string {
	if len(i.Data) > 0 {
		if lines := i.renderInline(width); len(lines) > 0 {
			return lines
		}
	}
	label := i.AltText
	if label == "" {
		label = "image"
	}
	return []string{TruncateToWidth(fmt.Sprintf("[Image: %s %dx%d]", label, i.Width, i.Height), width, "...")}
}

func (i *Image) renderInline(width int) []string {
	caps := GetCapabilities()
	if caps.Images == ImageProtocolNone && !caps.Kitty && !caps.ITerm2 {
		return nil
	}
	maxWidth := i.MaxWidth
	if maxWidth <= 0 || maxWidth > width {
		maxWidth = width
	}
	if maxWidth <= 0 {
		maxWidth = 1
	}
	maxHeight := i.MaxHeight
	dim := ImageDimensions{Width: i.Width, Height: i.Height}
	if dim.Width <= 0 || dim.Height <= 0 {
		if detected, err := GetImageDimensions(i.Data); err == nil {
			dim = detected
		}
	}
	size := ImageCellSize{Columns: maxWidth, Rows: max(1, i.Height)}
	if dim.Width > 0 && dim.Height > 0 {
		if maxHeight > 0 {
			size = CalculateImageCellSize(dim, maxWidth, maxHeight)
		} else {
			size = CalculateImageCellSize(dim, maxWidth)
		}
	}
	moveCursor := false
	seq := RenderImage(i.Data, ImageRenderOptions{
		Width:      size.Columns,
		Height:     size.Rows,
		MaxWidth:   maxWidth,
		MaxHeight:  maxHeight,
		MoveCursor: &moveCursor,
	})
	if seq == "" || !IsImageLine(seq) {
		return nil
	}
	lines := make([]string, max(1, size.Rows))
	lines[0] = seq
	for idx := 1; idx < len(lines); idx++ {
		lines[idx] = strings.Repeat(" ", min(width, maxWidth))
	}
	return lines
}
