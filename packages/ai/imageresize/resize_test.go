package imageresize

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestResizeDownscalesOversizedDimensions verifies an image larger than the max
// box is shrunk to fit and reports WasResized with a dimension note.
func TestResizeDownscalesOversizedDimensions(t *testing.T) {
	data := encodePNG(t, 2400, 1000)
	r := Resize(data, "image/png", Options{})
	if r == nil {
		t.Fatal("expected resized image, got nil")
	}
	if !r.WasResized {
		t.Fatal("expected WasResized=true")
	}
	if r.Width > 2000 || r.Height > 2000 {
		t.Fatalf("not within max box: %dx%d", r.Width, r.Height)
	}
	if r.OriginalWidth != 2400 || r.OriginalHeight != 1000 {
		t.Fatalf("original dims=%dx%d", r.OriginalWidth, r.OriginalHeight)
	}
	if DimensionNote(r) == "" {
		t.Fatal("expected dimension note for resized image")
	}
}

// TestResizePassthroughSmall verifies a small in-budget image is returned
// unchanged with no dimension note.
func TestResizePassthroughSmall(t *testing.T) {
	data := encodePNG(t, 16, 16)
	r := Resize(data, "image/png", Options{})
	if r == nil || r.WasResized {
		t.Fatalf("expected passthrough, got %#v", r)
	}
	if DimensionNote(r) != "" {
		t.Fatal("expected no dimension note when not resized")
	}
}

// TestResizeOmitsWhenImpossible verifies Resize returns nil when the image cannot
// be brought under the byte budget even at 1x1.
func TestResizeOmitsWhenImpossible(t *testing.T) {
	data := encodePNG(t, 16, 16)
	if r := Resize(data, "image/png", Options{MaxBytes: 10}); r != nil {
		t.Fatalf("expected nil (omit), got %#v", r)
	}
}

// TestResizeUndecodablePassthrough verifies a format the stdlib cannot decode is
// passed through when small and omitted when over budget.
func TestResizeUndecodablePassthrough(t *testing.T) {
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 32)...)
	if r := Resize(webp, "image/webp", Options{}); r == nil || r.WasResized {
		t.Fatalf("expected passthrough for small undecodable image, got %#v", r)
	}
	if r := Resize(webp, "image/webp", Options{MaxBytes: 4}); r != nil {
		t.Fatalf("expected omit for oversized undecodable image, got %#v", r)
	}
}
