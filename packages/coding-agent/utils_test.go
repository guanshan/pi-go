package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrontmatter(t *testing.T) {
	parsed, err := ParseFrontmatter("---\ntitle: Test\ncount: 2\ndraft: false\ntags: [a, b]\n---\n\nBody\n")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Frontmatter["title"] != "Test" || parsed.Frontmatter["count"] != 2 || parsed.Body != "Body" {
		t.Fatalf("parsed=%#v", parsed)
	}
	if body := StripFrontmatter("no frontmatter"); body != "no frontmatter" {
		t.Fatalf("body=%q", body)
	}
}

func TestMimeDetection(t *testing.T) {
	png := append([]byte{}, pngSignature...)
	png = append(png, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	if got := DetectSupportedImageMimeType(png); got != "image/png" {
		t.Fatalf("png=%q", got)
	}
	apng := append([]byte{}, pngSignature...)
	apng = append(apng, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	apng = append(apng, make([]byte, 17)...)
	apng = append(apng, 0, 0, 0, 0, 'a', 'c', 'T', 'L')
	if got := DetectSupportedImageMimeType(apng); got != "" {
		t.Fatalf("apng=%q", got)
	}
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xe0}); got != "image/jpeg" {
		t.Fatalf("jpeg=%q", got)
	}
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xf7}); got != "" {
		t.Fatalf("jpeg-ls=%q", got)
	}
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 16)...)
	if got := DetectSupportedImageMimeType(webp); got != "image/webp" {
		t.Fatalf("webp=%q", got)
	}
	path := filepath.Join(t.TempDir(), "image.gif")
	if err := os.WriteFile(path, []byte("GIF89a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectSupportedImageMimeTypeFromFile(path); err != nil || got != "image/gif" {
		t.Fatalf("gif=%q err=%v", got, err)
	}
}

func TestHTMLAndANSIUtilities(t *testing.T) {
	if got := DecodeHTMLEntity("amp"); got != "&" {
		t.Fatalf("entity=%q", got)
	}
	if got := DecodeHTMLEntity("copy"); got != "\u00a9" {
		t.Fatalf("copy entity=%q", got)
	}
	decoded, ok := DecodeHTMLEntityAt("a &lt; b", 2)
	if !ok || decoded.Text != "<" || decoded.Length != 4 {
		t.Fatalf("decoded=%#v ok=%v", decoded, ok)
	}
	if got := StripANSI("\x1b[31mred\x1b[0m"); got != "red" {
		t.Fatalf("stripped=%q", got)
	}
}

func TestPathAndShellUtilities(t *testing.T) {
	home := t.TempDir()
	expanded := NormalizePath("@~/x\u00a0y", PathInputOptions{
		Trim:                   true,
		HomeDir:                home,
		StripAtPrefix:          true,
		NormalizeUnicodeSpaces: true,
	})
	if expanded != filepath.Join(home, "x y") {
		t.Fatalf("expanded=%q", expanded)
	}
	cwd := t.TempDir()
	child := filepath.Join(cwd, "dir", "file.txt")
	if got := FormatPathRelativeToCWDOrAbsolute(child, cwd); got != "dir/file.txt" {
		t.Fatalf("relative=%q", got)
	}
	if IsLocalPath("https://example.com/repo.git") {
		t.Fatal("remote URL considered local")
	}
	env := GetShellEnv(home)
	if env["PATH"] == "" {
		t.Fatal("PATH missing")
	}
}

func TestSleepAndSanitize(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); err == nil || err.Error() != "Aborted" {
		t.Fatalf("sleep err=%v", err)
	}
	if got := SanitizeBinaryOutput("a\x00b\tc\ufffad"); got != "ab\tcd" {
		t.Fatalf("sanitized=%q", got)
	}
}
