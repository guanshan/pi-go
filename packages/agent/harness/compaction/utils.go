package compaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
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
// joined by ", ". TS uses `Object.entries(args)`, whose property order is
// array-index keys first (numeric ascending), then other string keys in insertion
// order, so we walk the raw JSON tokens and apply that ordering explicitly.
// Values are encoded with JSON.stringify semantics: no HTML escaping and
// "[unserializable]" on failure.
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
	var parts []jsonObjectMember
	seen := map[string]int{}
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
		member := jsonObjectMember{Key: key, Value: safeJSONStringify(value), Order: len(parts)}
		if idx, ok := seen[key]; ok {
			parts[idx].Value = member.Value
		} else {
			seen[key] = len(parts)
			parts = append(parts, member)
		}
	}
	orderJSONMembers(parts)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, part.Key+"="+part.Value)
	}
	return strings.Join(out, ", ")
}

// safeJSONStringify mirrors TS safeJsonStringify: it re-encodes the value with
// JSON.stringify semantics (compact, no HTML escaping, numbers in their JS
// canonical form) and falls back to "[unserializable]" when encoding fails. It
// walks the JSON token stream rather than decoding into a Go map so nested
// object members can be emitted in JS property order.
func safeJSONStringify(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var buf bytes.Buffer
	if err := writeCanonicalJSON(dec, &buf); err != nil {
		return "[unserializable]"
	}
	return buf.String()
}

type jsonObjectMember struct {
	Key   string
	Value string
	Order int
}

// writeCanonicalJSON reads exactly one JSON value from dec and writes its
// JSON.stringify-equivalent encoding to buf, preserving JS property order.
func writeCanonicalJSON(dec *json.Decoder, buf *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var members []jsonObjectMember
			seen := map[string]int{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyTok.(string)
				if !ok {
					return fmt.Errorf("unexpected non-string object key")
				}
				var value bytes.Buffer
				if err := writeCanonicalJSON(dec, &value); err != nil {
					return err
				}
				member := jsonObjectMember{Key: key, Value: value.String(), Order: len(members)}
				if idx, ok := seen[key]; ok {
					members[idx].Value = member.Value
				} else {
					seen[key] = len(members)
					members = append(members, member)
				}
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return err
			}
			orderJSONMembers(members)
			buf.WriteByte('{')
			for i, member := range members {
				if i > 0 {
					buf.WriteByte(',')
				}
				writeCanonicalString(buf, member.Key)
				buf.WriteByte(':')
				buf.WriteString(member.Value)
			}
			buf.WriteByte('}')
		case '[':
			buf.WriteByte('[')
			for i := 0; dec.More(); i++ {
				if i > 0 {
					buf.WriteByte(',')
				}
				if err := writeCanonicalJSON(dec, buf); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return err
			}
			buf.WriteByte(']')
		default:
			return fmt.Errorf("unexpected delimiter %q", t)
		}
	case json.Number:
		// Canonicalize via float64 so the literal matches JS Number formatting
		// (e.g. 1e10 -> 10000000000, 1.0 -> 1); Inf/NaN stringify to null, as JS does.
		f, err := t.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			buf.WriteString("null")
			return nil
		}
		encoded, err := json.Marshal(f)
		if err != nil {
			return err
		}
		buf.Write(encoded)
	case string:
		writeCanonicalString(buf, t)
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		return fmt.Errorf("unexpected token %T", tok)
	}
	return nil
}

func orderJSONMembers(members []jsonObjectMember) {
	sort.SliceStable(members, func(i, j int) bool {
		return jsPropertyKeyLess(members[i].Key, members[i].Order, members[j].Key, members[j].Order)
	})
}

func jsPropertyKeyLess(left string, leftOrder int, right string, rightOrder int) bool {
	leftIndex, leftIsIndex := jsArrayIndex(left)
	rightIndex, rightIsIndex := jsArrayIndex(right)
	switch {
	case leftIsIndex && rightIsIndex:
		return leftIndex < rightIndex
	case leftIsIndex:
		return true
	case rightIsIndex:
		return false
	default:
		return leftOrder < rightOrder
	}
}

func jsArrayIndex(key string) (uint32, bool) {
	const maxArrayIndex = uint64(1<<32 - 1)
	if key == "" {
		return 0, false
	}
	if key == "0" {
		return 0, true
	}
	if key[0] == '0' {
		return 0, false
	}
	var value uint64
	for _, ch := range key {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		digit := uint64(ch - '0')
		if value > (maxArrayIndex-digit)/10 {
			return 0, false
		}
		value = value*10 + digit
		if value >= maxArrayIndex {
			return 0, false
		}
	}
	return uint32(value), true
}

// writeCanonicalString writes s as a JSON string with JSON.stringify semantics:
// no HTML escaping and U+2028/U+2029 emitted literally (Go escapes them).
func writeCanonicalString(buf *bytes.Buffer, s string) {
	var sb bytes.Buffer
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		buf.WriteString(`""`)
		return
	}
	encoded := bytes.TrimSuffix(sb.Bytes(), []byte{'\n'})
	buf.Write(restoreJSONStringifySeparators(encoded))
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
