package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/ai/imageresize"
)

func (ReadTool) Name() string { return "read" }
func (ReadTool) Description() string {
	return "Read text files and images. Text output is truncated to 2000 lines or 50KB; use offset/limit to continue."
}
func (ReadTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"path":   stringSchema("Path to the file to read (relative or absolute)"),
		"offset": numberSchema("Line number to start reading from (1-indexed)"),
		"limit":  numberSchema("Maximum number of lines to read"),
	}, []string{"path"})
}
func (t ReadTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
		return toolError("Invalid read input: path is required")
	}
	abs := ResolveReadPath(t.CWD, args.Path)
	data, err := os.ReadFile(abs)
	if err != nil {
		return toolError(err.Error())
	}
	if isImagePath(abs, data) {
		mimeType := detectMime(abs, data)
		if !t.AutoResize {
			return ai.ToolResult{Content: []ai.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Read image file [%s]", mimeType)},
				{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType},
			}}
		}
		resized := imageresize.Resize(data, mimeType, imageresize.Options{})
		if resized == nil {
			return ai.ToolResult{Content: ai.TextBlocks(fmt.Sprintf("Read image file [%s]\n[Image omitted: could not be resized below the inline image size limit.]", mimeType))}
		}
		text := fmt.Sprintf("Read image file [%s]", resized.MimeType)
		if note := imageresize.DimensionNote(resized); note != "" {
			text += "\n" + note
		}
		return ai.ToolResult{Content: []ai.ContentBlock{
			{Type: "text", Text: text},
			{Type: "image", Data: resized.Data, MimeType: resized.MimeType},
		}}
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	if args.Offset > 0 {
		start := args.Offset - 1
		if start >= len(lines) {
			return toolError(fmt.Sprintf("Offset %d is beyond end of file (%d lines total)", args.Offset, len(lines)))
		}
		lines = lines[start:]
	}
	if args.Limit > 0 && args.Limit < len(lines) {
		lines = lines[:args.Limit]
		text = strings.Join(lines, "\n")
		next := args.Offset
		if next <= 0 {
			next = 1
		}
		next += args.Limit
		text += fmt.Sprintf("\n\n[%d more lines in file. Use offset=%d to continue.]", len(strings.Split(string(data), "\n"))-next+1, next)
	} else {
		text = strings.Join(lines, "\n")
	}
	trunc := TruncateHead(text, DefaultMaxLines, DefaultMaxBytes)
	if trunc.FirstLineExceedsLimit {
		offset := args.Offset
		if offset <= 0 {
			offset = 1
		}
		text = fmt.Sprintf("[Line %d exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]", offset, FormatSize(DefaultMaxBytes), offset, args.Path, DefaultMaxBytes)
	} else if trunc.Truncated {
		start := args.Offset
		if start <= 0 {
			start = 1
		}
		end := start + trunc.OutputLines - 1
		text = trunc.Content + fmt.Sprintf("\n\n[Showing lines %d-%d. Use offset=%d to continue.]", start, end, end+1)
	} else {
		text = trunc.Content
	}
	var details any
	if trunc.Truncated {
		details = map[string]any{"truncation": trunc}
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: details}
}
