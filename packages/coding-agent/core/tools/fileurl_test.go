package tools

import (
	"path/filepath"
	"testing"
)

// TestFileURLToPath covers the shared file:// parser, in particular the Windows
// drive-letter handling that the old bare url.Parse(...).Path lost (it produced
// the invalid /C:/work/x.txt). Expectations use filepath.FromSlash so they hold
// on both Unix and Windows, mirroring Node's fileURLToPath (paths.ts:74-76).
func TestFileURLToPath(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"file:///etc/x", filepath.FromSlash("/etc/x"), true},
		{"file://localhost/etc/x", filepath.FromSlash("/etc/x"), true},
		// The drive letter must NOT keep a leading slash.
		{"file:///C:/work/x.txt", filepath.FromSlash("C:/work/x.txt"), true},
		{"file:C:%5Cwork%5Cx.txt", filepath.FromSlash("C:\\work\\x.txt"), true},
		{"not-a-url", "", false},
		{"/plain/path", "", false},
	}
	for _, c := range cases {
		got, ok := FileURLToPath(c.raw)
		if ok != c.wantOK {
			t.Fatalf("FileURLToPath(%q) ok=%v want %v", c.raw, ok, c.wantOK)
		}
		if ok && got != c.want {
			t.Fatalf("FileURLToPath(%q)=%q want %q", c.raw, got, c.want)
		}
	}
}
