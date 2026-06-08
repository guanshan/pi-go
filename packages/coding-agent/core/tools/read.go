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
	// Byte-exact with read.ts:212 (DEFAULT_MAX_LINES=2000, DEFAULT_MAX_BYTES/1024=50).
	return "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete."
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
		// Limit is a pointer so an explicit limit:0 (empty selection) is
		// distinguished from an absent limit (TruncateHead default), matching
		// read.ts:291 `if (limit !== undefined)`.
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
		return toolError("Invalid read input: path is required")
	}
	abs := ResolveReadPath(t.CWD, args.Path)
	data, err := os.ReadFile(abs)
	if err != nil {
		return toolError(err.Error())
	}
	// Mirror read.ts getNonVisionImageNote: when the active model lacks "image"
	// input, append a note that the image will be omitted from the request.
	nonVisionImageNote := ""
	if !t.ModelSupportsImages {
		nonVisionImageNote = "[Current model does not support images. The image will be omitted from this request.]"
	}
	// Mirror read.ts:243-247: classify the file purely by content sniffing
	// (utils/mime.ts detectSupportedImageMimeType). A non-empty MIME means the
	// content is a supported inline image; otherwise the file is read as text.
	if mimeType := detectMime(abs, data); mimeType != "" {
		if !t.AutoResize {
			textNote := fmt.Sprintf("Read image file [%s]", mimeType)
			if nonVisionImageNote != "" {
				textNote += "\n" + nonVisionImageNote
			}
			return ai.ToolResult{Content: []ai.ContentBlock{
				{Type: "text", Text: textNote},
				{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType},
			}}
		}
		resized := imageresize.Resize(data, mimeType, imageresize.Options{})
		if resized == nil {
			textNote := fmt.Sprintf("Read image file [%s]\n[Image omitted: could not be resized below the inline image size limit.]", mimeType)
			if nonVisionImageNote != "" {
				textNote += "\n" + nonVisionImageNote
			}
			return ai.ToolResult{Content: ai.TextBlocks(textNote)}
		}
		text := fmt.Sprintf("Read image file [%s]", resized.MimeType)
		if note := imageresize.DimensionNote(resized); note != "" {
			text += "\n" + note
		}
		if nonVisionImageNote != "" {
			text += "\n" + nonVisionImageNote
		}
		return ai.ToolResult{Content: []ai.ContentBlock{
			{Type: "text", Text: text},
			{Type: "image", Data: resized.Data, MimeType: resized.MimeType},
		}}
	}
	textContent := string(data)
	allLines := strings.Split(textContent, "\n")
	totalFileLines := len(allLines)
	startLine := 0
	if args.Offset > 0 {
		startLine = args.Offset - 1
	}
	startLineDisplay := startLine + 1
	if startLine >= len(allLines) {
		return toolError(fmt.Sprintf("Offset %d is beyond end of file (%d lines total)", args.Offset, len(allLines)))
	}
	// Mirror read.ts:288-297: honor an explicit limit first (limit:0 -> empty
	// selection), then let TruncateHead decide. Distinguish absent from 0 via the
	// decoded *int pointer.
	var selectedContent string
	userLimited := false
	userLimitedLines := 0
	if args.Limit != nil {
		end := startLine + *args.Limit
		if end > len(allLines) {
			end = len(allLines)
		}
		if end < startLine {
			end = startLine
		}
		selectedContent = strings.Join(allLines[startLine:end], "\n")
		userLimited = true
		userLimitedLines = end - startLine
	} else {
		selectedContent = strings.Join(allLines[startLine:], "\n")
	}
	trunc := TruncateHead(selectedContent, DefaultMaxLines, DefaultMaxBytes)
	var text string
	var details any
	switch {
	case trunc.FirstLineExceedsLimit:
		// Mirror TS read.ts:303-304: report the offending line's byte size.
		firstLineSize := FormatSize(len(allLines[startLine]))
		text = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]", startLineDisplay, firstLineSize, FormatSize(DefaultMaxBytes), startLineDisplay, args.Path, DefaultMaxBytes)
		details = map[string]any{"truncation": trunc}
	case trunc.Truncated:
		end := startLineDisplay + trunc.OutputLines - 1
		nextOffset := end + 1
		// Mirror TS read.ts:311-315: include "of TOTAL" and, for byte
		// truncation, the byte-limit suffix.
		if trunc.TruncatedBy == "bytes" {
			text = trunc.Content + fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]", startLineDisplay, end, totalFileLines, FormatSize(DefaultMaxBytes), nextOffset)
		} else {
			text = trunc.Content + fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", startLineDisplay, end, totalFileLines, nextOffset)
		}
		details = map[string]any{"truncation": trunc}
	case userLimited && startLine+userLimitedLines < len(allLines):
		// Mirror TS read.ts:317-321: a user-specified limit stopped early but the
		// file still has more content. This note never mixes with the truncation
		// notes above (else-if chain).
		remaining := len(allLines) - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		text = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", trunc.Content, remaining, nextOffset)
	default:
		text = trunc.Content
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: details}
}
