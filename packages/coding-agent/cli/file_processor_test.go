package cli

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInitialMessage(t *testing.T) {
	text, images, remaining := BuildInitialMessageWithRemaining(InitialMessageInput{
		Parsed:       Args{Messages: []string{"Explain it", "Ignored"}},
		FileText:     "<file name=\"x\">file</file>\n",
		StdinContent: "stdin\n",
	})
	if text != "stdin\n<file name=\"x\">file</file>\nExplain it" {
		t.Fatalf("text=%q", text)
	}
	if len(images) != 0 {
		t.Fatalf("images=%#v", images)
	}
	if len(remaining) != 1 || remaining[0] != "Ignored" {
		t.Fatalf("remaining=%#v", remaining)
	}
}

func TestProcessFileArgumentsTextImageAndReadPathVariants(t *testing.T) {
	cwd := t.TempDir()
	textPath := filepath.Join(cwd, "prompt.txt")
	imagePath := filepath.Join(cwd, "image.png")
	screenshotPath := filepath.Join(cwd, "Screen Shot 2026-05-28 at 10.00.00\u202fAM.txt")
	curlyPath := filepath.Join(cwd, "Capture d\u2019ecran.txt")
	fileURLPath := filepath.Join(cwd, "from url.txt")
	for path, content := range map[string][]byte{
		textPath: []byte("plain text"),
		// Valid minimal PNG (signature + IHDR length 13); a bare signature is
		// treated as text by the TS-faithful sniffer (utils/mime.ts requires IHDR).
		imagePath: {
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
			0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		},
		screenshotPath: []byte("screenshot"),
		curlyPath:      []byte("curly"),
		fileURLPath:    []byte("file url"),
	} {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	processed, err := ProcessFileArguments(cwd, []string{
		"prompt.txt",
		"image.png",
		"Screen Shot 2026-05-28 at 10.00.00 AM.txt",
		"Capture d'ecran.txt",
		(&url.URL{Scheme: "file", Path: fileURLPath}).String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plain text", "screenshot", "curly", "file url"} {
		if !strings.Contains(processed.Text, want) {
			t.Fatalf("text missing %q: %q", want, processed.Text)
		}
	}
	if len(processed.Images) != 1 || processed.Images[0].MimeType != "image/png" || processed.Images[0].Data == "" {
		t.Fatalf("images=%#v", processed.Images)
	}
}

// TestProcessFileArgumentsMissingFileWording matches file-processor.ts: a
// missing @file/--file reports "File not found: <path>" (not the raw os error).
func TestProcessFileArgumentsMissingFileWording(t *testing.T) {
	cwd := t.TempDir()
	_, err := ProcessFileArguments(cwd, []string{"does-not-exist.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.HasPrefix(err.Error(), "File not found: ") {
		t.Fatalf("error = %q, want prefix %q", err.Error(), "File not found: ")
	}
	if !strings.Contains(err.Error(), "does-not-exist.txt") {
		t.Fatalf("error should include the path, got %q", err.Error())
	}
}

func TestFileURLPathHandlesWindowsDriveForms(t *testing.T) {
	for _, raw := range []string{
		"file:///C:/work/from%20url.txt",
		"file://C:/work/from%20url.txt",
		"file://C:%5Cwork%5Cfrom%20url.txt",
		"file:C:%5Cwork%5Cfrom%20url.txt",
		`file:\C:%5Cwork%5Cfrom%20url.txt`,
	} {
		got, ok := fileURLPath(raw)
		if !ok {
			t.Fatalf("fileURLPath(%q) failed", raw)
		}
		if normalized := strings.ReplaceAll(got, "\\", "/"); normalized != "C:/work/from url.txt" {
			t.Fatalf("fileURLPath(%q)=%q", raw, got)
		}
	}
}

func TestParseConfigSelection(t *testing.T) {
	indexes, err := ParseConfigSelection("1, 2 2\t3", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 3 || indexes[0] != 1 || indexes[1] != 2 || indexes[2] != 3 {
		t.Fatalf("indexes=%#v", indexes)
	}
	if _, err := ParseConfigSelection("4", 3); err == nil {
		t.Fatal("expected range error")
	}
}
