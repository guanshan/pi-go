package imageresize

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	// Register decoders so image.Decode handles PNG/JPEG/GIF inputs.
	_ "image/gif"
)

// Options controls Resize. Zero fields fall back to the defaults that mirror the
// upstream image-resize-core defaults.
type Options struct {
	MaxWidth    int // default 2000
	MaxHeight   int // default 2000
	MaxBytes    int // default 4.5MB of base64 payload (below Anthropic's 5MB limit)
	JPEGQuality int // default 80
}

// Result is the outcome of Resize: base64 data plus the dimensions before/after,
// mirroring the upstream ResizedImage shape.
type Result struct {
	Data           string
	MimeType       string
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
	WasResized     bool
}

const defaultMaxBytes = 4.5 * 1024 * 1024

func (o Options) withDefaults() Options {
	if o.MaxWidth <= 0 {
		o.MaxWidth = 2000
	}
	if o.MaxHeight <= 0 {
		o.MaxHeight = 2000
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = int(defaultMaxBytes)
	}
	if o.JPEGQuality <= 0 {
		o.JPEGQuality = 80
	}
	return o
}

// Resize shrinks an image to fit within the configured max dimensions and base64
// payload size, returning nil when it cannot be brought under MaxBytes. It is a
// stdlib port of the upstream resizeImageInProcess: try the original if it
// already fits, otherwise downscale to the max box and pick the smallest of PNG /
// decreasing-quality JPEG, progressively reducing dimensions if needed.
//
// Inputs whose format the standard library cannot decode (e.g. WebP) are passed
// through unchanged when already under MaxBytes, or omitted (nil) when not.
func Resize(input []byte, mimeType string, opts Options) *Result {
	o := opts.withDefaults()
	inputBase64Size := base64.StdEncoding.EncodedLen(len(input))

	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		// Undecodable format: keep it if it already fits, otherwise omit.
		if inputBase64Size < o.MaxBytes {
			return &Result{
				Data:       base64.StdEncoding.EncodeToString(input),
				MimeType:   mimeType,
				WasResized: false,
			}
		}
		return nil
	}

	bounds := img.Bounds()
	originalWidth, originalHeight := bounds.Dx(), bounds.Dy()

	if originalWidth <= o.MaxWidth && originalHeight <= o.MaxHeight && inputBase64Size < o.MaxBytes {
		return &Result{
			Data:           base64.StdEncoding.EncodeToString(input),
			MimeType:       mimeType,
			OriginalWidth:  originalWidth,
			OriginalHeight: originalHeight,
			Width:          originalWidth,
			Height:         originalHeight,
			WasResized:     false,
		}
	}

	targetWidth, targetHeight := originalWidth, originalHeight
	if targetWidth > o.MaxWidth {
		targetHeight = int(float64(targetHeight) * float64(o.MaxWidth) / float64(targetWidth))
		targetWidth = o.MaxWidth
	}
	if targetHeight > o.MaxHeight {
		targetWidth = int(float64(targetWidth) * float64(o.MaxHeight) / float64(targetHeight))
		targetHeight = o.MaxHeight
	}

	qualities := dedupeQualities(o.JPEGQuality, 85, 70, 55, 40)
	currentWidth, currentHeight := targetWidth, targetHeight
	for {
		if currentWidth < 1 {
			currentWidth = 1
		}
		if currentHeight < 1 {
			currentHeight = 1
		}
		resized := downscaleNRGBA(img, currentWidth, currentHeight)
		for _, candidate := range encodeCandidates(resized, qualities) {
			if base64.StdEncoding.EncodedLen(len(candidate.bytes)) < o.MaxBytes {
				return &Result{
					Data:           base64.StdEncoding.EncodeToString(candidate.bytes),
					MimeType:       candidate.mimeType,
					OriginalWidth:  originalWidth,
					OriginalHeight: originalHeight,
					Width:          currentWidth,
					Height:         currentHeight,
					WasResized:     true,
				}
			}
		}
		if currentWidth == 1 && currentHeight == 1 {
			return nil
		}
		nextWidth := max(1, currentWidth*3/4)
		nextHeight := max(1, currentHeight*3/4)
		if nextWidth == currentWidth && nextHeight == currentHeight {
			return nil
		}
		currentWidth, currentHeight = nextWidth, nextHeight
	}
}

// DimensionNote returns the note that maps resized-image coordinates back to the
// original, or "" when the image was not resized. It mirrors the upstream
// formatDimensionNote string exactly.
func DimensionNote(r *Result) string {
	if r == nil || !r.WasResized || r.Width == 0 {
		return ""
	}
	scale := float64(r.OriginalWidth) / float64(r.Width)
	return fmt.Sprintf("[Image: original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.]",
		r.OriginalWidth, r.OriginalHeight, r.Width, r.Height, scale)
}

type candidate struct {
	bytes    []byte
	mimeType string
}

// encodeCandidates returns a PNG candidate followed by JPEG candidates at the
// given qualities, matching the upstream candidate ordering (PNG preferred, then
// descending JPEG quality).
func encodeCandidates(img image.Image, qualities []int) []candidate {
	candidates := make([]candidate, 0, 1+len(qualities))
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err == nil {
		candidates = append(candidates, candidate{bytes: append([]byte(nil), pngBuf.Bytes()...), mimeType: "image/png"})
	}
	for _, q := range qualities {
		var jpgBuf bytes.Buffer
		if err := jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: q}); err == nil {
			candidates = append(candidates, candidate{bytes: append([]byte(nil), jpgBuf.Bytes()...), mimeType: "image/jpeg"})
		}
	}
	return candidates
}

func dedupeQualities(values ...int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// downscaleNRGBA shrinks src to dstW x dstH using area averaging, which gives
// good quality for the downscaling this package performs (it never upscales).
// Alpha is preserved via straight (non-premultiplied) NRGBA.
func downscaleNRGBA(src image.Image, dstW, dstH int) *image.NRGBA {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	if srcW == 0 || srcH == 0 {
		return dst
	}
	for dy := 0; dy < dstH; dy++ {
		sy0 := dy * srcH / dstH
		sy1 := (dy + 1) * srcH / dstH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < dstW; dx++ {
			sx0 := dx * srcW / dstW
			sx1 := (dx + 1) * srcW / dstW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a, count uint64
			for yy := sy0; yy < sy1; yy++ {
				for xx := sx0; xx < sx1; xx++ {
					c := color.NRGBAModel.Convert(src.At(b.Min.X+xx, b.Min.Y+yy)).(color.NRGBA)
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					a += uint64(c.A)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: uint8(r / count),
				G: uint8(g / count),
				B: uint8(bl / count),
				A: uint8(a / count),
			})
		}
	}
	return dst
}
