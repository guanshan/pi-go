package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveToolPathStripsAtPrefix mirrors the TS resolveToCwd behavior
// (stripAtPrefix:true) used by write/edit/grep/find/ls: a bare leading "@" is
// stripped so "@dir/file" resolves to the real "dir/file".
func TestResolveToolPathStripsAtPrefix(t *testing.T) {
	cwd := t.TempDir()
	got := ResolveToolPath(cwd, "@dir/file.txt")
	want := filepath.Join(cwd, "dir/file.txt")
	if got != want {
		t.Fatalf("ResolveToolPath(@dir/file.txt) = %q, want %q", got, want)
	}
}

// TestResolveToolPathKeepsLiteralAtPath ensures a literal "./@file" keeps its
// "@" because only a bare leading "@" is stripped.
func TestResolveToolPathKeepsLiteralAtPath(t *testing.T) {
	cwd := t.TempDir()
	got := ResolveToolPath(cwd, "./@literal.txt")
	want := filepath.Join(cwd, "@literal.txt")
	if got != want {
		t.Fatalf("ResolveToolPath(./@literal.txt) = %q, want %q", got, want)
	}
}

// TestToolsStripAtPrefix verifies each file/dir tool resolves "@dir/file" to
// the real "dir/file" on disk.
func TestToolsStripAtPrefix(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "dir", "file.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("write", func(t *testing.T) {
		result := WriteTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path": "@out/created.txt", "content": "hello",
		}), nil)
		if result.IsError {
			t.Fatalf("write failed: %s", toolText(result.Content))
		}
		data, err := os.ReadFile(filepath.Join(cwd, "out", "created.txt"))
		if err != nil || string(data) != "hello" {
			t.Fatalf("write did not create out/created.txt: data=%q err=%v", data, err)
		}
	})

	t.Run("edit", func(t *testing.T) {
		result := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path":  "@dir/file.txt",
			"edits": []map[string]any{{"oldText": "needle", "newText": "thread"}},
		}), nil)
		if result.IsError {
			t.Fatalf("edit failed: %s", toolText(result.Content))
		}
		data, _ := os.ReadFile(filepath.Join(cwd, "dir", "file.txt"))
		if !strings.Contains(string(data), "thread") {
			t.Fatalf("edit did not modify real dir/file.txt: %s", data)
		}
	})

	t.Run("ls", func(t *testing.T) {
		result := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "@dir"}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "file.txt") {
			t.Fatalf("ls @dir failed: %#v", result)
		}
	})

	t.Run("find", func(t *testing.T) {
		result := FindTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"pattern": "*.txt", "path": "@dir",
		}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "file.txt") {
			t.Fatalf("find @dir failed: %#v", result)
		}
	})

	t.Run("grep", func(t *testing.T) {
		result := GrepTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"pattern": "thread", "path": "@dir",
		}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "file.txt") {
			t.Fatalf("grep @dir failed: %#v", result)
		}
	})
}

// TestToolsKeepLiteralAtPath verifies that a literal "./@name" path (a real
// file/dir whose name starts with "@") stays accessible because only a bare
// leading "@" is stripped.
func TestToolsKeepLiteralAtPath(t *testing.T) {
	cwd := t.TempDir()
	atDir := filepath.Join(cwd, "@literal")
	if err := os.MkdirAll(atDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(atDir, "note.txt"), []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("ls", func(t *testing.T) {
		result := LsTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{"path": "./@literal"}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "note.txt") {
			t.Fatalf("ls ./@literal failed: %#v", result)
		}
	})

	t.Run("find", func(t *testing.T) {
		result := FindTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"pattern": "*.txt", "path": "./@literal",
		}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "note.txt") {
			t.Fatalf("find ./@literal failed: %#v", result)
		}
	})

	t.Run("grep", func(t *testing.T) {
		result := GrepTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"pattern": "kept", "path": "./@literal",
		}), nil)
		if result.IsError || !strings.Contains(toolText(result.Content), "note.txt") {
			t.Fatalf("grep ./@literal failed: %#v", result)
		}
	})

	t.Run("write", func(t *testing.T) {
		result := WriteTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path": "./@literal/new.txt", "content": "x",
		}), nil)
		if result.IsError {
			t.Fatalf("write ./@literal/new.txt failed: %s", toolText(result.Content))
		}
		if _, err := os.Stat(filepath.Join(atDir, "new.txt")); err != nil {
			t.Fatalf("write did not target literal @literal dir: %v", err)
		}
	})

	t.Run("edit", func(t *testing.T) {
		result := EditTool{CWD: cwd}.Execute(context.Background(), raw(map[string]any{
			"path":  "./@literal/note.txt",
			"edits": []map[string]any{{"oldText": "kept", "newText": "KEPT"}},
		}), nil)
		if result.IsError {
			t.Fatalf("edit ./@literal/note.txt failed: %s", toolText(result.Content))
		}
		data, _ := os.ReadFile(filepath.Join(atDir, "note.txt"))
		if !strings.Contains(string(data), "KEPT") {
			t.Fatalf("edit did not modify literal @literal/note.txt: %s", data)
		}
	})
}
