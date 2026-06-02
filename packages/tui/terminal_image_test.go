package tui

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestCalculateImageCellSizeAspectRatio(t *testing.T) {
	SetCellDimensions(CellDimensions{Width: 9, Height: 18})

	// 100x100 image at maxWidth=80 cells: image is "square-ish", but cells are
	// taller than wide so the image height in pixels would be 80*9 = 720 px,
	// then rows = 720 / 18 = 40 rows. Columns clamped to 80.
	got := CalculateImageCellSize(ImageDimensions{Width: 100, Height: 100}, 80)
	if got.Columns != 80 {
		t.Errorf("100x100 cols: %d", got.Columns)
	}
	if got.Rows != 40 {
		t.Errorf("100x100 rows: %d (want 40)", got.Rows)
	}

	// Wide image: 1600x100 at maxWidth=80 → fills 80 cols, rows ≈ 80*9*100/(1600*18) ≈ 2.5 → 3
	got = CalculateImageCellSize(ImageDimensions{Width: 1600, Height: 100}, 80)
	if got.Columns != 80 {
		t.Errorf("wide cols: %d", got.Columns)
	}
	if got.Rows < 2 || got.Rows > 4 {
		t.Errorf("wide rows: %d (want 2-4)", got.Rows)
	}

	// Tall image with maxHeight constraint: should clamp.
	got = CalculateImageCellSize(ImageDimensions{Width: 100, Height: 1000}, 80, 10)
	if got.Rows != 10 {
		t.Errorf("tall constrained rows: %d", got.Rows)
	}
}

func TestCalculateImageRowsAspectScaling(t *testing.T) {
	// At fixed maxWidth=80, a wider image should produce fewer rows than a
	// taller image of the same width.
	SetCellDimensions(CellDimensions{Width: 9, Height: 18})

	wide := CalculateImageRows(ImageDimensions{Width: 100, Height: 50}, 80)
	tall := CalculateImageRows(ImageDimensions{Width: 100, Height: 100}, 80)
	if wide >= tall {
		t.Errorf("expected fewer rows for wider image: wide=%d tall=%d", wide, tall)
	}
}

func TestAllocateImageIDUnique(t *testing.T) {
	a := AllocateImageID()
	b := AllocateImageID()
	if a == b {
		t.Errorf("ids collided: %d %d", a, b)
	}
}

func TestEncodeKittyChunkStructure(t *testing.T) {
	// Single APC when the base64 payload fits in one chunk: control keys and
	// payload travel together, no m= marker.
	small := EncodeKitty([]byte("hi"), ImageRenderOptions{ID: 5})
	if strings.Count(small, "\x1b_G") != 1 {
		t.Fatalf("small payload should be a single APC: %q", small)
	}
	if strings.Contains(small, "m=1") || strings.Contains(small, "m=0") {
		t.Fatalf("single chunk must not carry m= marker: %q", small)
	}

	// Payload large enough to span at least 3 base64 chunks of 4096 chars each
	// (3*4096=12288 base64 chars need > 9216 raw bytes; use 12000 to be safe).
	seq := EncodeKitty(bytes.Repeat([]byte{0xAB}, 12000), ImageRenderOptions{ID: 7})
	chunks := strings.Split(seq, "\x1b\\")
	// Trailing empty element from the final terminator split.
	if chunks[len(chunks)-1] == "" {
		chunks = chunks[:len(chunks)-1]
	}
	if len(chunks) < 3 {
		t.Fatalf("expected >=3 chunks, got %d: %q", len(chunks), seq)
	}
	for i, chunk := range chunks {
		body, ok := strings.CutPrefix(chunk, "\x1b_G")
		if !ok {
			t.Fatalf("chunk %d missing kitty prefix: %q", i, chunk)
		}
		ctrl, payload, ok := strings.Cut(body, ";")
		if !ok {
			t.Fatalf("chunk %d missing control/payload separator: %q", i, chunk)
		}
		switch i {
		case 0:
			// First chunk carries every control key plus m=1; the others must not.
			if !strings.Contains(ctrl, "a=T") || !strings.Contains(ctrl, "i=7") || !strings.HasSuffix(ctrl, "m=1") {
				t.Fatalf("first chunk control keys wrong: %q", ctrl)
			}
		case len(chunks) - 1:
			if ctrl != "m=0" {
				t.Fatalf("last chunk control must be bare m=0, got: %q", ctrl)
			}
		default:
			if ctrl != "m=1" {
				t.Fatalf("middle chunk control must be bare m=1, got: %q", ctrl)
			}
		}
		if len(payload) > 4096 {
			t.Fatalf("chunk %d payload exceeds 4096 chars: %d", i, len(payload))
		}
	}
}

func TestImageComponentRendersInlineWhenDataAndCapabilitiesAllow(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	SetCellDimensions(CellDimensions{Width: 10, Height: 10})
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 20))); err != nil {
		t.Fatal(err)
	}
	lines := (&Image{AltText: "diagram", Data: buf.Bytes(), Width: 10, Height: 20, MaxWidth: 8}).Render(12)
	if len(lines) < 2 {
		t.Fatalf("expected inline image rows, got %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "\x1b_G") {
		t.Fatalf("first line is not kitty image: %q", lines[0])
	}

	SetCapabilities(TerminalCapabilities{})
	lines = (&Image{AltText: "diagram", Data: buf.Bytes(), Width: 10, Height: 20}).Render(12)
	if len(lines) != 1 || !strings.Contains(lines[0], "[Image: d") {
		t.Fatalf("fallback lines=%#v", lines)
	}
}
