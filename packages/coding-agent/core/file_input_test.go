package core

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

func TestPrepareInitialMessageMatchesTypeScriptBuildInitialMessage(t *testing.T) {
	cwd := t.TempDir()
	text, images, err := PrepareInitialMessage(cwd, cli.Args{Messages: []string{"Summarize the text given"}}, "README contents\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if text != "README contents\nSummarize the text given" {
		t.Fatalf("initial text=%q", text)
	}
	if len(images) != 0 {
		t.Fatalf("images=%#v", images)
	}

	text, _, err = PrepareInitialMessage(cwd, cli.Args{}, "README contents", true)
	if err != nil {
		t.Fatal(err)
	}
	if text != "README contents" {
		t.Fatalf("stdin-only text=%q", text)
	}
}

func TestPrepareInitialMessageUsesFirstMessageAndFileTags(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "prompt.txt")
	if err := os.WriteFile(path, []byte("file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, images, err := PrepareInitialMessage(cwd, cli.Args{
		FileArgs: []string{"prompt.txt"},
		Messages: []string{"Explain it", "Second message"},
	}, "stdin\n", true)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Clean(path)
	want := "stdin\n" + `<file name="` + wantPath + `">` + "\nfile\n\n</file>\nExplain it"
	if text != want {
		t.Fatalf("text=%q\nwant=%q", text, want)
	}
	if strings.Contains(text, "Second message") {
		t.Fatalf("included second message: %q", text)
	}
	if len(images) != 0 {
		t.Fatalf("images=%#v", images)
	}

	prepared, err := PrepareInitialPrompt(cwd, cli.Args{Messages: []string{"first", "second"}}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Message != "first" || len(prepared.Remaining) != 1 || prepared.Remaining[0] != "second" {
		t.Fatalf("prepared=%#v", prepared)
	}
}

func TestPrepareInitialMessageImageFileTagAndAttachment(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "image.png")
	// A valid minimal PNG (8-byte signature + IHDR chunk of length 13). The TS
	// image sniffer (utils/mime.ts) requires a valid IHDR, so a bare signature
	// is treated as text; this fixture exercises the image-attachment path.
	data := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	text, images, err := PrepareInitialMessage(cwd, cli.Args{FileArgs: []string{"image.png"}}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + filepath.Clean(path) + `"></file>` + "\n"
	if text != want {
		t.Fatalf("image text=%q want=%q", text, want)
	}
	if len(images) != 1 || images[0].MimeType != "image/png" || images[0].Data == "" {
		t.Fatalf("images=%#v", images)
	}
}

func TestPrepareInitialMessageResolvesReadPathVariants(t *testing.T) {
	cwd := t.TempDir()
	screenshotPath := filepath.Join(cwd, "Screen Shot 2026-05-28 at 10.00.00\u202fAM.txt")
	curlyQuotePath := filepath.Join(cwd, "Capture d\u2019ecran.txt")
	fileURLPath := filepath.Join(cwd, "from url.txt")
	for path, content := range map[string]string{
		screenshotPath: "screenshot",
		curlyQuotePath: "curly quote",
		fileURLPath:    "file url",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	text, images, err := PrepareInitialMessage(cwd, cli.Args{FileArgs: []string{
		"Screen Shot 2026-05-28 at 10.00.00 AM.txt",
		"Capture d'ecran.txt",
		(&url.URL{Scheme: "file", Path: fileURLPath}).String(),
	}}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"screenshot", "curly quote", "file url"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q: %q", want, text)
		}
	}
	if len(images) != 0 {
		t.Fatalf("images=%#v", images)
	}
}
