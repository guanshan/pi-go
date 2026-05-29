package tui

import "testing"

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
