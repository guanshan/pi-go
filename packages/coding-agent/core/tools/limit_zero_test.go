package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestReadLimitZeroSelectsNoLines mirrors read.ts:291-297: an explicit limit:0
// produces an empty line selection (distinct from an absent limit which lets
// TruncateHead decide).
func TestReadLimitZeroSelectsNoLines(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "f.txt", "limit": 0}), nil)
	if res.IsError {
		t.Fatalf("read errored: %s", toolText(res.Content))
	}
	out := toolText(res.Content)
	// limit:0 -> empty selection; output is just the user-limit continuation note
	// (no file content before the blank-line separator).
	if !strings.HasPrefix(out, "\n\n[") {
		t.Fatalf("limit:0 should select no content before the note, got %q", out)
	}
	if !strings.Contains(out, "more lines in file") {
		t.Fatalf("expected user-limit continuation note, got %q", out)
	}
}

// TestReadAbsentLimitReturnsAllLines confirms an absent limit returns the file
// content (pointer distinguishes absent from 0).
func TestReadAbsentLimitReturnsAllLines(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "f.txt"}), nil)
	out := toolText(res.Content)
	if !strings.Contains(out, "a") || !strings.Contains(out, "c") {
		t.Fatalf("absent limit should return all lines, got %q", out)
	}
}

// TestReadNonVisionImageNote mirrors read.ts getNonVisionImageNote: when the
// model lacks image input, the image read appends the omission note.
func TestReadNonVisionImageNote(t *testing.T) {
	cwd := t.TempDir()
	// 1x1 transparent PNG.
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(filepath.Join(cwd, "img.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	const note = "[Current model does not support images. The image will be omitted from this request.]"

	withNote := ReadTool{CWD: cwd, AutoResize: false, ModelSupportsImages: false}.Execute(context.Background(), raw(map[string]any{"path": "img.png"}), nil)
	if !strings.Contains(toolText(withNote.Content), note) {
		t.Fatalf("non-vision model should append omission note, got %q", toolText(withNote.Content))
	}
	withVision := ReadTool{CWD: cwd, AutoResize: false, ModelSupportsImages: true}.Execute(context.Background(), raw(map[string]any{"path": "img.png"}), nil)
	if strings.Contains(toolText(withVision.Content), note) {
		t.Fatalf("vision model should not append omission note, got %q", toolText(withVision.Content))
	}
}

// TestLsLimitZeroIsEmpty mirrors ls.ts:125,156: an explicit limit:0 yields no
// entries ("(empty directory)").
func TestLsLimitZeroIsEmpty(t *testing.T) {
	cwd := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(cwd, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": ".", "limit": 0}), nil)
	if got := toolText(res.Content); got != "(empty directory)" {
		t.Fatalf("ls limit:0 should be empty, got %q", got)
	}
}

// TestFindLimitZeroIsEmpty mirrors find.ts effectiveLimit 0 (fd --max-results 0).
func TestFindLimitZeroIsEmpty(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := FindTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"pattern": "*.go", "limit": 0}), nil)
	if got := toolText(res.Content); got != "No files found matching pattern" {
		t.Fatalf("find limit:0 should be empty, got %q", got)
	}
}

// TestLsLocaleSort verifies entries order case-insensitively and locale-aware
// (ls.ts:150 toLowerCase().localeCompare).
func TestLsLocaleSort(t *testing.T) {
	cwd := t.TempDir()
	for _, n := range []string{"Banana", "apple", "Cherry"} {
		if err := os.WriteFile(filepath.Join(cwd, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "."}), nil)
	out := toolText(res.Content)
	apple := strings.Index(out, "apple")
	banana := strings.Index(out, "Banana")
	cherry := strings.Index(out, "Cherry")
	if apple >= banana || banana >= cherry {
		t.Fatalf("expected case-insensitive order apple<Banana<Cherry, got %q", out)
	}
}

func TestLsLocaleSortConcurrent(t *testing.T) {
	cwd := t.TempDir()
	for _, n := range []string{"Banana", "apple", "Cherry", "delta", "Echo"} {
		if err := os.WriteFile(filepath.Join(cwd, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				res := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "."}), nil)
				if res.IsError {
					t.Errorf("ls errored: %s", toolText(res.Content))
					return
				}
			}
		}()
	}
	wg.Wait()
}
