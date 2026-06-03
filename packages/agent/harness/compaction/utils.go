package compaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

type FileOperations struct {
	Read    map[string]struct{} `json:"read"`
	Written map[string]struct{} `json:"written"`
	Edited  map[string]struct{} `json:"edited"`
}

func CreateFileOps() FileOperations {
	return FileOperations{
		Read:    map[string]struct{}{},
		Written: map[string]struct{}{},
		Edited:  map[string]struct{}{},
	}
}

func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	if fileOps == nil {
		return
	}
	if _, ok := ai.AsAssistantMessage(message); !ok {
		return
	}
	ensureFileOps(fileOps)
	for _, block := range ai.MessageBlocks(message) {
		if block.Type != "toolCall" || block.Name == "" || len(block.Arguments) == 0 {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(block.Arguments, &args); err != nil {
			continue
		}
		path, _ := args["path"].(string)
		if path == "" {
			continue
		}
		switch block.Name {
		case "read":
			fileOps.Read[path] = struct{}{}
		case "write":
			fileOps.Written[path] = struct{}{}
		case "edit":
			fileOps.Edited[path] = struct{}{}
		}
	}
}

func ComputeFileLists(fileOps FileOperations) (readFiles, modifiedFiles []string) {
	modified := map[string]struct{}{}
	for file := range fileOps.Edited {
		modified[file] = struct{}{}
	}
	for file := range fileOps.Written {
		modified[file] = struct{}{}
	}
	for file := range fileOps.Read {
		if _, ok := modified[file]; !ok {
			readFiles = append(readFiles, file)
		}
	}
	for file := range modified {
		modifiedFiles = append(modifiedFiles, file)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles
}

func FormatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func SerializeConversation(messages []ai.Message) string {
	parts := []string{}
	for _, message := range messages {
		switch message.(type) {
		case ai.UserMessage, *ai.UserMessage:
			// TS joins user text blocks with "" (not "\n"); mirror that exactly
			// so the serialized prompt is byte-for-byte identical.
			if text := joinTextBlocks(ai.MessageBlocks(message)); text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case ai.AssistantMessage, *ai.AssistantMessage:
			var textParts, thinkingParts, toolCalls []string
			for _, block := range ai.MessageBlocks(message) {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "thinking":
					thinkingParts = append(thinkingParts, block.Thinking)
				case "toolCall":
					toolCalls = append(toolCalls, block.Name+"("+formatToolCallArgs(block.Arguments)+")")
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case ai.ToolResultMessage, *ai.ToolResultMessage:
			// TS joins tool-result text blocks with "" as well.
			if text := joinTextBlocks(ai.MessageBlocks(message)); text != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(text, 2000))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// joinTextBlocks concatenates the text of every text block with no separator,
// matching the TS serializeConversation user/toolResult content handling
// (`content.filter(...).map((c) => c.text).join("")`).
func joinTextBlocks(blocks []ai.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// formatToolCallArgs serializes a tool call's arguments as `k=<json>` pairs
// joined by ", ". TS uses `Object.entries(args)` which preserves the JSON
// object's key insertion order, so we walk the raw JSON tokens in order rather
// than decoding into a Go map (which would lose ordering). Values are encoded
// with JSON.stringify semantics: no HTML escaping and "[unserializable]" on
// failure.
func formatToolCallArgs(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return string(raw)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return string(raw)
	}
	var parts []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return string(raw)
		}
		key, ok := keyTok.(string)
		if !ok {
			return string(raw)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return string(raw)
		}
		parts = append(parts, key+"="+safeJSONStringify(value))
	}
	return strings.Join(parts, ", ")
}

// safeJSONStringify mirrors TS safeJsonStringify: it re-encodes the value with
// JSON.stringify semantics (compact, no HTML escaping) and falls back to
// "[unserializable]" when encoding fails.
func safeJSONStringify(raw json.RawMessage) string {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return "[unserializable]"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return "[unserializable]"
	}
	encoded := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	return string(restoreJSONStringifySeparators(encoded))
}

var (
	jsonEscapeLineSeparator      = []byte(`\u2028`)
	jsonEscapeParagraphSeparator = []byte(`\u2029`)
	jsonLineSeparatorUTF8        = []byte{0xe2, 0x80, 0xa8}
	jsonParagraphSeparatorUTF8   = []byte{0xe2, 0x80, 0xa9}
)

func restoreJSONStringifySeparators(raw []byte) []byte {
	if !bytes.Contains(raw, jsonEscapeLineSeparator) && !bytes.Contains(raw, jsonEscapeParagraphSeparator) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		switch {
		case hasJSONEscapeAt(raw, i, jsonEscapeLineSeparator) && !jsonBackslashIsEscaped(raw, i):
			out = append(out, jsonLineSeparatorUTF8...)
			i += len(jsonEscapeLineSeparator)
		case hasJSONEscapeAt(raw, i, jsonEscapeParagraphSeparator) && !jsonBackslashIsEscaped(raw, i):
			out = append(out, jsonParagraphSeparatorUTF8...)
			i += len(jsonEscapeParagraphSeparator)
		default:
			out = append(out, raw[i])
			i++
		}
	}
	return out
}

func hasJSONEscapeAt(raw []byte, offset int, escape []byte) bool {
	return offset+len(escape) <= len(raw) && bytes.Equal(raw[offset:offset+len(escape)], escape)
}

func jsonBackslashIsEscaped(raw []byte, offset int) bool {
	backslashes := 0
	for i := offset - 1; i >= 0 && raw[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// truncateForSummary mirrors TS truncateForSummary, which measures and slices in
// UTF-16 code units (text.length / text.slice). Slicing on UTF-16 boundaries can
// split an astral rune mid-surrogate in JS; in Go we never split a rune, so we
// cut at the last whole rune that stays within the maxChars UTF-16 budget. The
// reported "more characters truncated" count is computed in UTF-16 units to
// match the TS message.
func truncateForSummary(text string, maxChars int) string {
	total := utf16Len(text)
	if total <= maxChars {
		return text
	}
	head, headUnits := utf16Prefix(text, maxChars)
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", head, total-headUnits)
}

// utf16Prefix returns the longest rune-aligned prefix of s whose UTF-16 length
// does not exceed maxUnits, along with that prefix's UTF-16 length.
func utf16Prefix(s string, maxUnits int) (string, int) {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > maxUnits {
			return s[:i], units
		}
		units += w
	}
	return s, units
}

func ensureFileOps(fileOps *FileOperations) {
	if fileOps.Read == nil {
		fileOps.Read = map[string]struct{}{}
	}
	if fileOps.Written == nil {
		fileOps.Written = map[string]struct{}{}
	}
	if fileOps.Edited == nil {
		fileOps.Edited = map[string]struct{}{}
	}
}
