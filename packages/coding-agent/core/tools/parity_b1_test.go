package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- P1-13: read tool image detection via content sniffing ---------------

// validPNGHeader returns a minimal valid PNG header: 8-byte signature, an IHDR
// chunk with length 13 (no acTL), enough to satisfy isPNG and not look animated.
func validPNGHeader() []byte {
	b := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	// IHDR chunk: length(4)=13, "IHDR"(4), 13 bytes of data, CRC(4).
	b = append(b, 0x00, 0x00, 0x00, 0x0d)
	b = append(b, 'I', 'H', 'D', 'R')
	b = append(b, make([]byte, 13)...)
	b = append(b, 0, 0, 0, 0) // CRC
	// IDAT chunk so the animated scan terminates with "not animated".
	b = append(b, 0x00, 0x00, 0x00, 0x00)
	b = append(b, 'I', 'D', 'A', 'T')
	b = append(b, 0, 0, 0, 0)
	return b
}

func TestDetectMimeIgnoresExtension(t *testing.T) {
	// A .png file whose bytes are plain text must be read as text (mime ""),
	// matching TS content-only sniffing (utils/mime.ts).
	if got := detectMime("foo.png", []byte("this is not a png, just text\n")); got != "" {
		t.Fatalf("text content with .png extension should not detect as image, got %q", got)
	}
	// Conversely, valid PNG bytes with a non-image extension must detect as PNG.
	if got := detectMime("foo.txt", validPNGHeader()); got != "image/png" {
		t.Fatalf("PNG bytes with .txt extension should detect image/png, got %q", got)
	}
}

func TestDetectMimeRejectsJPEGVariant0xF7(t *testing.T) {
	// JPEG-LS / the byte[3]==0xf7 variant is rejected by TS.
	jls := []byte{0xff, 0xd8, 0xff, 0xf7, 0x00, 0x00}
	if got := detectMime("a.jpg", jls); got != "" {
		t.Fatalf("byte[3]==0xf7 JPEG variant must be rejected, got %q", got)
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x00}
	if got := detectMime("a.jpg", jpeg); got != "image/jpeg" {
		t.Fatalf("normal JPEG must detect image/jpeg, got %q", got)
	}
}

func TestDetectMimeRejectsAnimatedPNG(t *testing.T) {
	// PNG with an acTL chunk before IDAT is animated and must be rejected.
	b := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	b = append(b, 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R')
	b = append(b, make([]byte, 13)...)
	b = append(b, 0, 0, 0, 0)
	// acTL chunk (animation control) before IDAT.
	b = append(b, 0x00, 0x00, 0x00, 0x08, 'a', 'c', 'T', 'L')
	b = append(b, make([]byte, 8)...)
	b = append(b, 0, 0, 0, 0)
	if got := detectMime("anim.png", b); got != "" {
		t.Fatalf("animated PNG (acTL before IDAT) must be rejected, got %q", got)
	}
}

func TestDetectMimeRejectsBadIHDR(t *testing.T) {
	// PNG signature but IHDR length != 13 must be rejected.
	b := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	b = append(b, 0x00, 0x00, 0x00, 0x0c, 'I', 'H', 'D', 'R') // length 12, not 13
	b = append(b, make([]byte, 16)...)
	if got := detectMime("bad.png", b); got != "" {
		t.Fatalf("PNG with IHDR length != 13 must be rejected, got %q", got)
	}
}

func TestReadToolTextFileWithImageExtension(t *testing.T) {
	// A file named .png whose content is text must be returned as text, not an
	// image attachment (TS reads it as text).
	cwd := t.TempDir()
	p := filepath.Join(cwd, "note.png")
	if err := os.WriteFile(p, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := ReadTool{CWD: cwd, AutoResize: true, ModelSupportsImages: true}.Execute(
		context.Background(), raw(map[string]any{"path": "note.png"}), nil)
	if res.IsError {
		t.Fatalf("read failed: %s", toolText(res.Content))
	}
	for _, c := range res.Content {
		if c.Type == "image" {
			t.Fatalf("text file with .png extension must not be attached as image")
		}
	}
	if !strings.Contains(toolText(res.Content), "hello world") {
		t.Fatalf("expected text content, got %q", toolText(res.Content))
	}
}

// --- P1-14: edit tool 'diff' detail via generateDiffString ----------------

func TestGenerateDiffStringReplace(t *testing.T) {
	old := "a\nb\nc\nd\ne\n"
	updated := "a\nb\nX\nd\ne\n"
	diff, first := generateDiffString(old, updated, 4)
	want := strings.Join([]string{
		" 1 a",
		" 2 b",
		"-3 c",
		"+3 X",
		" 4 d",
		" 5 e",
	}, "\n")
	if diff != want {
		t.Fatalf("diff mismatch\n got:\n%s\nwant:\n%s", diff, want)
	}
	if first != 3 {
		t.Fatalf("firstChangedLine = %d, want 3", first)
	}
}

func TestGenerateDiffStringSkipMarkerAndPadding(t *testing.T) {
	// Build a file large enough that an unchanged run between two changes exceeds
	// contextLines*2, forcing a " ... " skip marker, and line numbers cross 10 so
	// padding (width 2) shows.
	var oldLines, newLines []string
	for i := 1; i <= 14; i++ {
		oldLines = append(oldLines, "line"+itoa(i))
		newLines = append(newLines, "line"+itoa(i))
	}
	// Change first and last lines so a large unchanged middle sits between them.
	oldLines[0] = "OLD-first"
	newLines[0] = "NEW-first"
	oldLines[13] = "OLD-last"
	newLines[13] = "NEW-last"
	old := strings.Join(oldLines, "\n") + "\n"
	updated := strings.Join(newLines, "\n") + "\n"

	diff, first := generateDiffString(old, updated, 4)
	if first != 1 {
		t.Fatalf("firstChangedLine = %d, want 1", first)
	}
	// Right-padded line numbers (width 2 since max line is 14), leading context
	// lines prefixed by a space, and a " ... " skip marker around the unchanged
	// run that exceeds contextLines*2 lines.
	want := strings.Join([]string{
		"- 1 OLD-first",
		"+ 1 NEW-first",
		"  2 line2",
		"  3 line3",
		"  4 line4",
		"  5 line5",
		"    ...",
		" 10 line10",
		" 11 line11",
		" 12 line12",
		" 13 line13",
		"-14 OLD-last",
		"+14 NEW-last",
	}, "\n")
	if diff != want {
		t.Fatalf("diff mismatch\n got:\n%s\nwant:\n%s", diff, want)
	}
}

func TestEditToolDiffDetailUsesGenerateDiffString(t *testing.T) {
	cwd := t.TempDir()
	p := filepath.Join(cwd, "f.txt")
	if err := os.WriteFile(p, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := EditTool{CWD: cwd}.Execute(context.Background(),
		raw(map[string]any{"path": "f.txt", "edits": []map[string]any{{"oldText": "c", "newText": "X"}}}), nil)
	if res.IsError {
		t.Fatalf("edit failed: %s", toolText(res.Content))
	}
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("details not a map: %T", res.Details)
	}
	diff, _ := details["diff"].(string)
	wantDiff, _ := generateDiffString("a\nb\nc\nd\ne\n", "a\nb\nX\nd\ne\n", 4)
	if diff != wantDiff {
		t.Fatalf("edit diff detail mismatch\n got:\n%s\nwant:\n%s", diff, wantDiff)
	}
	if fcl, _ := details["firstChangedLine"].(int); fcl != 3 {
		t.Fatalf("firstChangedLine = %v, want 3", details["firstChangedLine"])
	}
}

// --- P2-25: only the edit tool emits additionalProperties:false -----------

func TestSchemaAdditionalPropertiesOnlyOnEdit(t *testing.T) {
	type schemaTool interface{ Schema() map[string]any }
	nonStrict := map[string]schemaTool{
		"read":  ReadTool{},
		"write": WriteTool{},
		"grep":  GrepTool{},
		"find":  FindTool{},
		"ls":    LsTool{},
		"bash":  BashTool{},
	}
	for name, tool := range nonStrict {
		s := tool.Schema()
		if _, ok := s["additionalProperties"]; ok {
			t.Fatalf("%s schema must NOT include additionalProperties", name)
		}
	}
	edit := EditTool{}.Schema()
	if v, ok := edit["additionalProperties"]; !ok || v != false {
		t.Fatalf("edit schema must include additionalProperties:false, got %v (present=%v)", v, ok)
	}
	// The nested edits item schema must also be strict (editSchema:41).
	props, _ := edit["properties"].(map[string]any)
	edits, _ := props["edits"].(map[string]any)
	item, _ := edits["items"].(map[string]any)
	if v, ok := item["additionalProperties"]; !ok || v != false {
		t.Fatalf("edit edits item schema must include additionalProperties:false, got %v (present=%v)", v, ok)
	}
}

// --- P2-26: grep limit clamping (Math.max(1, limit ?? DEFAULT)) -----------

func TestGrepLimitZeroClampsToOne(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("NEEDLE\n")
	}
	writeFile(t, root, "a.txt", b.String())
	res := GrepTool{CWD: root}.Execute(context.Background(),
		raw(map[string]any{"pattern": "NEEDLE", "limit": 0}), nil)
	got := toolText(res.Content)
	// limit:0 clamps to 1, so exactly one match line is shown before the notice.
	if !strings.HasPrefix(got, "a.txt:1: NEEDLE\n") {
		t.Fatalf("limit:0 should yield exactly 1 match, got:\n%s", got)
	}
	if strings.Count(got, "NEEDLE") != 1 {
		t.Fatalf("limit:0 should yield exactly 1 match line, got:\n%s", got)
	}
	if !strings.Contains(got, "[1 matches limit reached. Use limit=2 for more, or refine pattern]") {
		t.Fatalf("expected effective-limit-based notice, got:\n%s", got)
	}
}

func TestGrepLimitAbsentUsesDefault(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteString("NEEDLE\n")
	}
	writeFile(t, root, "a.txt", b.String())
	res := GrepTool{CWD: root}.Execute(context.Background(),
		raw(map[string]any{"pattern": "NEEDLE"}), nil)
	got := toolText(res.Content)
	if strings.Count(got, "NEEDLE") != 3 {
		t.Fatalf("absent limit should default to 100 (all 3 shown), got:\n%s", got)
	}
}

// --- P3-26: formatRel basename fallback for out-of-tree matches -----------

func TestFormatRelBasenameWhenOutsideRoot(t *testing.T) {
	root := "/tmp/searchdir"
	// A matched file outside root yields a "../"-prefixed relative path; TS falls
	// back to the bare basename.
	if got := formatRel("/tmp/other/file.go", root, true); got != "file.go" {
		t.Fatalf("out-of-tree match should use basename, got %q", got)
	}
	// In-tree matches keep the slash-joined relative path.
	if got := formatRel("/tmp/searchdir/sub/file.go", root, true); got != "sub/file.go" {
		t.Fatalf("in-tree match should use relative path, got %q", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
