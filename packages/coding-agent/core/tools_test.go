package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func toolText(content []ai.ContentBlock) string {
	return ai.MessageText(ai.ToolResultMessage{Content: content})
}

func TestReadWriteEditLsFindGrepTools(t *testing.T) {
	cwd := t.TempDir()
	write := catools.WriteTool{CWD: cwd}
	if result := write.Execute(context.Background(), raw(map[string]any{"path": "src/app.txt", "content": "alpha\nbeta\ngamma\n"}), nil); result.IsError {
		t.Fatalf("write failed: %s", toolText(result.Content))
	}
	read := catools.ReadTool{CWD: cwd}
	result := read.Execute(context.Background(), raw(map[string]any{"path": "src/app.txt", "offset": 2, "limit": 1}), nil)
	if result.IsError || !strings.Contains(toolText(result.Content), "beta") {
		t.Fatalf("read failed: %#v", result)
	}
	edit := catools.EditTool{CWD: cwd}
	result = edit.Execute(context.Background(), raw(map[string]any{"path": "src/app.txt", "edits": []map[string]any{{"oldText": "beta", "newText": "BETA"}}}), nil)
	if result.IsError {
		t.Fatalf("edit failed: %s", toolText(result.Content))
	}
	data, _ := os.ReadFile(filepath.Join(cwd, "src/app.txt"))
	if !strings.Contains(string(data), "BETA") {
		t.Fatalf("edit did not write: %s", data)
	}
	ls := catools.LsTool{CWD: cwd}
	result = ls.Execute(context.Background(), raw(map[string]any{"path": "."}), nil)
	if result.IsError || !strings.Contains(toolText(result.Content), "src/") {
		t.Fatalf("ls failed: %#v", result)
	}
	find := catools.FindTool{CWD: cwd}
	result = find.Execute(context.Background(), raw(map[string]any{"pattern": "**/*.txt"}), nil)
	if result.IsError || !strings.Contains(toolText(result.Content), "src/app.txt") {
		t.Fatalf("find failed: %#v", result)
	}
	grep := catools.GrepTool{CWD: cwd}
	result = grep.Execute(context.Background(), raw(map[string]any{"pattern": "BETA", "path": "."}), nil)
	if result.IsError || !strings.Contains(toolText(result.Content), "app.txt") {
		t.Fatalf("grep failed: %#v", result)
	}
}

func TestBuiltinToolsUseSettingsAgentBinDir(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := NewSettingsManager(cwd, agentDir)
	tools := BuiltinToolsForModel(cwd, settings, true)
	want := filepath.Join(agentDir, "bin")

	for name, tool := range tools {
		switch typed := tool.(type) {
		case catools.BashTool:
			if typed.BinDir != want {
				t.Fatalf("%s BinDir=%q, want %q", name, typed.BinDir, want)
			}
		case catools.GrepTool:
			if typed.BinDir != want {
				t.Fatalf("%s BinDir=%q, want %q", name, typed.BinDir, want)
			}
		case catools.FindTool:
			if typed.BinDir != want {
				t.Fatalf("%s BinDir=%q, want %q", name, typed.BinDir, want)
			}
		}
	}
}
