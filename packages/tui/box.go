package tui

import (
	"strings"
	"sync"
)

// Box is a Container that applies horizontal/vertical padding and an
// optional background style to its children. Render output is cached when
// children, width, and bg-sample are unchanged.
type Box struct {
	Container
	PaddingX int
	PaddingY int
	Bg       func(string) string

	mu    sync.Mutex
	cache *boxCache
}

type boxCache struct {
	width      int
	bgSample   string
	childLines []string
	output     []string
}

// NewBox constructs a Box.
func NewBox(paddingX, paddingY int, bg func(string) string) *Box {
	return &Box{PaddingX: paddingX, PaddingY: paddingY, Bg: bg}
}

// Invalidate drops the render cache (and propagates to children).
func (b *Box) Invalidate() {
	b.mu.Lock()
	b.cache = nil
	b.mu.Unlock()
	b.Container.Invalidate()
}

// SetBgFn updates the background-styling function. Cache is sampled on each
// Render to detect bg changes, so SetBgFn does not need explicit invalidation.
func (b *Box) SetBgFn(fn func(string) string) {
	b.Bg = fn
}

// Render produces the framed lines for the given width.
func (b *Box) Render(width int) []string {
	if len(b.Children) == 0 {
		return []string{}
	}
	inner := width - b.PaddingX*2
	if inner < 1 {
		inner = 1
	}
	leftPad := strings.Repeat(" ", b.PaddingX)

	// Render children once.
	childLines := make([]string, 0, 32)
	for _, child := range b.Children {
		for _, line := range child.Render(inner) {
			childLines = append(childLines, leftPad+line)
		}
	}
	if len(childLines) == 0 {
		return []string{}
	}

	// Cache key: width + bgSample + child lines.
	bgSample := ""
	if b.Bg != nil {
		bgSample = b.Bg("test")
	}
	b.mu.Lock()
	if b.cache != nil &&
		b.cache.width == width &&
		b.cache.bgSample == bgSample &&
		stringSliceEq(b.cache.childLines, childLines) {
		out := b.cache.output
		b.mu.Unlock()
		return out
	}
	b.mu.Unlock()

	// Build output.
	out := make([]string, 0, len(childLines)+b.PaddingY*2)
	for i := 0; i < b.PaddingY; i++ {
		out = append(out, b.applyBg("", width))
	}
	for _, line := range childLines {
		padded := line + strings.Repeat(" ", b.PaddingX)
		out = append(out, b.applyBg(padded, width))
	}
	for i := 0; i < b.PaddingY; i++ {
		out = append(out, b.applyBg("", width))
	}

	b.mu.Lock()
	b.cache = &boxCache{
		width:      width,
		bgSample:   bgSample,
		childLines: append([]string(nil), childLines...),
		output:     append([]string(nil), out...),
	}
	b.mu.Unlock()
	return out
}

func (b *Box) applyBg(line string, width int) string {
	visLen := VisibleWidth(line)
	pad := width - visLen
	if pad < 0 {
		pad = 0
	}
	padded := line + strings.Repeat(" ", pad)
	if b.Bg != nil {
		return b.Bg(padded)
	}
	return padded
}

func stringSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
