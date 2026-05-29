package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestEditAcceptsStringifiedEdits verifies the edit tool tolerates models that
// send the edits array as a JSON string rather than a real array.
func TestEditAcceptsStringifiedEdits(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit := EditTool{CWD: cwd}
	stringified := `[{"oldText":"beta","newText":"BETA"}]`
	result := edit.Execute(context.Background(), raw(map[string]any{"path": "f.txt", "edits": stringified}), nil)
	if result.IsError {
		t.Fatalf("edit with stringified edits failed: %s", toolText(result.Content))
	}
	data, _ := os.ReadFile(filepath.Join(cwd, "f.txt"))
	if !strings.Contains(string(data), "BETA") {
		t.Fatalf("edit did not apply: %s", data)
	}
}

// TestWriteMutationQueueSerializesSameFile runs many concurrent writes to the
// same file; the per-file mutation queue must keep each write atomic so the
// final content is exactly one of the written payloads (never interleaved).
func TestWriteMutationQueueSerializesSameFile(t *testing.T) {
	cwd := t.TempDir()
	write := WriteTool{CWD: cwd}
	const n = 30
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := strings.Repeat("x", 1000) + "-" + string(rune('A'+i%26))
			payload, _ := json.Marshal(map[string]any{"path": "shared.txt", "content": content})
			if r := write.Execute(context.Background(), payload, nil); r.IsError {
				t.Errorf("write %d failed: %s", i, toolText(r.Content))
			}
		}(i)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(cwd, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if len(got) != 1002 || !strings.HasPrefix(got, strings.Repeat("x", 1000)+"-") {
		t.Fatalf("file content was interleaved by concurrent writes: %q (len %d)", got, len(got))
	}
}

func TestWritePreservesExistingMode(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "secret.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := WriteTool{CWD: cwd}
	result := write.Execute(context.Background(), raw(map[string]any{"path": "secret.txt", "content": "new"}), nil)
	if result.IsError {
		t.Fatalf("write failed: %s", toolText(result.Content))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%#o, want 0600", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(cwd, ".secret.txt.tmp-*")); len(matches) != 0 {
		t.Fatalf("atomic temp files left behind: %v", matches)
	}
}

func TestEditPreservesExistingMode(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "script.sh")
	if err := os.WriteFile(path, []byte("echo old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	edit := EditTool{CWD: cwd}
	result := edit.Execute(context.Background(), raw(map[string]any{
		"path":  "script.sh",
		"edits": []map[string]string{{"oldText": "old", "newText": "new"}},
	}), nil)
	if result.IsError {
		t.Fatalf("edit failed: %s", toolText(result.Content))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode=%#o, want 0755", got)
	}
}
