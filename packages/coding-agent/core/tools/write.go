package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guanshan/pi-go/packages/ai"
)

func (WriteTool) Name() string { return "write" }
func (WriteTool) Description() string {
	return "Create or overwrite a file, creating parent directories automatically."
}
func (WriteTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"path":    stringSchema("Path to the file to write"),
		"content": stringSchema("Content to write to the file"),
	}, []string{"path", "content"})
}
func (t WriteTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
		return toolError("Invalid write input: path and content are required")
	}
	abs := ResolveToolPath(t.CWD, args.Path)
	return withFileMutationQueue(abs, func() ai.ToolResult {
		if err := ctx.Err(); err != nil {
			return toolError(err.Error())
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return toolError(err.Error())
		}
		if err := atomicWriteFile(abs, []byte(args.Content), fileWriteMode(abs, 0o644)); err != nil {
			return toolError(err.Error())
		}
		return ai.ToolResult{Content: ai.TextBlocks(fmt.Sprintf("Successfully wrote %d bytes to %s", len(args.Content), args.Path))}
	})
}
