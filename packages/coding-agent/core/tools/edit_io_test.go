package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// TestNodeErrorCodeMapping exercises the errno -> Node error-code mapping
// directly, so EACCES/EISDIR coverage holds even when the filesystem-level
// EACCES subtest skips (e.g. running as root, where chmod bits are ignored).
func TestNodeErrorCodeMapping(t *testing.T) {
	cases := map[syscall.Errno]string{
		syscall.EACCES: "EACCES",
		syscall.EISDIR: "EISDIR",
		syscall.ENOENT: "ENOENT",
	}
	for errno, want := range cases {
		if got := nodeErrorCode(errno); got != want {
			t.Errorf("nodeErrorCode(%v) = %q, want %q", errno, got, want)
		}
	}
}

// utf8BOM is the byte sequence written at the start of a UTF-8 file with a byte
// order mark. It is constructed from the U+FEFF escape because a literal BOM is
// not allowed inside a Go source file.
const utf8BOM = "\uFEFF"

// runEditAt writes initialContent to <cwd>/<name>, runs the edit tool against
// that relative path, and returns (resultText, isError, finalFileBytes). Unlike
// runEdit it lets a test choose the filename and inspect the exact bytes on disk
// so byte-for-byte CRLF/BOM expectations can be asserted.
func runEditAt(t *testing.T, cwd, name, initialContent string, edits []map[string]any) (string, bool, string) {
	t.Helper()
	abs := filepath.Join(cwd, name)
	if err := os.WriteFile(abs, []byte(initialContent), 0o644); err != nil {
		t.Fatal(err)
	}
	res := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
		"path":  name,
		"edits": edits,
	}), nil)
	data, _ := os.ReadFile(abs)
	return toolText(res.Content), res.IsError, string(data)
}

// TestEditPreservesCRLFAndBOM mirrors the TS "edit tool CRLF handling" suite
// (test/tools.test.ts:985-1075). The Go edit tool normalizes content to LF for
// matching (edit.go:58-65 stripBOM/detectLineEnding/normalizeToLF) and then
// restores the detected line ending and re-prepends the BOM (edit.go:65,
// restoreLineEndings + edit.go:105-126). These cases assert that an LF oldText
// matches CRLF file content and that the file's original CRLF endings and UTF-8
// BOM survive the edit byte-for-byte.
func TestEditPreservesCRLFAndBOM(t *testing.T) {
	cwd := t.TempDir()

	// TS "should match LF oldText against CRLF file content" + "should preserve
	// CRLF line endings after edit" (lines 997-1021): an LF oldText matches a
	// CRLF file and the result keeps CRLF endings.
	t.Run("preserve-crlf", func(t *testing.T) {
		out, isErr, final := runEditAt(t, cwd, "crlf-preserve.txt",
			"first\r\nsecond\r\nthird\r\n",
			[]map[string]any{{"oldText": "second\n", "newText": "REPLACED\n"}},
		)
		if isErr {
			t.Fatalf("expected CRLF edit to succeed, got error: %s", out)
		}
		if want := "first\r\nREPLACED\r\nthird\r\n"; final != want {
			t.Fatalf("CRLF not preserved:\n got: %q\nwant: %q", final, want)
		}
	})

	// TS "should preserve LF line endings for LF files" (lines 1023-1034): a
	// pure-LF file stays LF (no spurious CRLF introduced).
	t.Run("preserve-lf", func(t *testing.T) {
		out, isErr, final := runEditAt(t, cwd, "lf-preserve.txt",
			"first\nsecond\nthird\n",
			[]map[string]any{{"oldText": "second\n", "newText": "REPLACED\n"}},
		)
		if isErr {
			t.Fatalf("expected LF edit to succeed, got error: %s", out)
		}
		if want := "first\nREPLACED\nthird\n"; final != want {
			t.Fatalf("LF not preserved:\n got: %q\nwant: %q", final, want)
		}
	})

	// TS "should detect duplicates across CRLF/LF variants" (lines 1036-1047):
	// a CRLF block and an LF block of the same text count as two occurrences
	// once normalized, so a single-edit oldText is rejected as duplicate.
	t.Run("crlf-lf-duplicate", func(t *testing.T) {
		out, isErr, _ := runEditAt(t, cwd, "mixed-endings.txt",
			"hello\r\nworld\r\n---\r\nhello\nworld\n",
			[]map[string]any{{"oldText": "hello\nworld\n", "newText": "replaced\n"}},
		)
		if !isErr {
			t.Fatalf("expected duplicate error across CRLF/LF variants, got success: %s", out)
		}
		if want := "Found 2 occurrences of the text in mixed-endings.txt. The text must be unique. Please provide more context to make it unique."; out != want {
			t.Fatalf("duplicate message mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	// TS "should preserve UTF-8 BOM after edit" (lines 1049-1060): a BOM-prefixed
	// CRLF file keeps both its BOM and its CRLF endings.
	t.Run("preserve-bom", func(t *testing.T) {
		out, isErr, final := runEditAt(t, cwd, "bom-test.txt",
			utf8BOM+"first\r\nsecond\r\nthird\r\n",
			[]map[string]any{{"oldText": "second\n", "newText": "REPLACED\n"}},
		)
		if isErr {
			t.Fatalf("expected BOM edit to succeed, got error: %s", out)
		}
		if want := utf8BOM + "first\r\nREPLACED\r\nthird\r\n"; final != want {
			t.Fatalf("BOM/CRLF not preserved:\n got: %q\nwant: %q", final, want)
		}
	})

	// TS "should preserve CRLF line endings and BOM in multi-edit mode"
	// (lines 1062-1076): multiple non-overlapping edits on a BOM+CRLF file keep
	// the BOM and CRLF endings across every replaced block.
	t.Run("preserve-bom-crlf-multi", func(t *testing.T) {
		out, isErr, final := runEditAt(t, cwd, "bom-crlf-multi.txt",
			utf8BOM+"first\r\nsecond\r\nthird\r\nfourth\r\n",
			[]map[string]any{
				{"oldText": "second\n", "newText": "SECOND\n"},
				{"oldText": "fourth\n", "newText": "FOURTH\n"},
			},
		)
		if isErr {
			t.Fatalf("expected multi-edit to succeed, got error: %s", out)
		}
		if want := utf8BOM + "first\r\nSECOND\r\nthird\r\nFOURTH\r\n"; final != want {
			t.Fatalf("multi-edit BOM/CRLF not preserved:\n got: %q\nwant: %q", final, want)
		}
	})
}

// permissionsEnforced reports whether the filesystem actually denies reads on a
// no-permission file. When the test runs as root (the common CI/container case)
// the mode bits are ignored and reads succeed, so the EACCES expectation cannot
// hold; callers t.Skip in that situation.
func permissionsEnforced(t *testing.T) bool {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "perm-probe.txt")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(probe, 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := os.ReadFile(probe)
	return err != nil
}

// TestEditSurfacesEACCESAndEISDIR mirrors the TS edit access-error cases
// (test/tools.test.ts:389-435): editing a path that cannot be read must surface
// the Node-style "Error code: <CODE>" message. The Go tool reads the file up
// front and maps the OS errno to a Node error code (edit.go:53-55 +
// editAccessErrorMessage/nodeErrorCode at edit.go:199-230).
func TestEditSurfacesEACCESAndEISDIR(t *testing.T) {
	// EISDIR: editing a directory. os.ReadFile on a directory returns EISDIR on
	// every supported platform (Windows reports a different errno, so this is
	// POSIX-only). There is no dedicated TS test for this, but it exercises the
	// same nodeErrorCode(EISDIR) branch the TS tool relies on.
	t.Run("eisdir-on-directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not return EISDIR for ReadFile on a directory")
		}
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, "subdir"), 0o755); err != nil {
			t.Fatal(err)
		}
		res := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path":  "subdir",
			"edits": []map[string]any{{"oldText": "a", "newText": "b"}},
		}), nil)
		out := toolText(res.Content)
		if want := "Could not edit file: subdir. Error code: EISDIR."; !res.IsError || out != want {
			t.Fatalf("EISDIR mismatch:\n got: %q\nwant: %q", out, want)
		}
	})

	// EACCES: editing an unreadable file. Mirrors the TS diff-preview variant
	// (lines 428-435, chmod 0o222) which expects "Error code: EACCES." Because
	// the Go tool reads before writing, an unreadable file is what surfaces
	// EACCES through editAccessErrorMessage. chmod is not enforced when running
	// as root, so guard with a runtime probe and skip if perms are ignored.
	t.Run("eacces-on-unreadable-file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows chmod does not model POSIX read permissions")
		}
		if !permissionsEnforced(t) {
			t.Skip("filesystem does not enforce permission bits (likely running as root); cannot trigger EACCES")
		}
		cwd := t.TempDir()
		abs := filepath.Join(cwd, "unreadable.txt")
		if err := os.WriteFile(abs, []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// 0o222 = write-only, mirroring the TS unreadable-preview fixture.
		if err := os.Chmod(abs, 0o222); err != nil {
			t.Fatal(err)
		}
		res := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path":  "unreadable.txt",
			"edits": []map[string]any{{"oldText": "hello", "newText": "world"}},
		}), nil)
		out := toolText(res.Content)
		if want := "Could not edit file: unreadable.txt. Error code: EACCES."; !res.IsError || out != want {
			t.Fatalf("EACCES mismatch:\n got: %q\nwant: %q", out, want)
		}
	})
}
