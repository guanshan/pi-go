package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

func (EditTool) Name() string { return "edit" }
func (EditTool) Description() string {
	return "Edit one file using exact, unique text replacements. Multiple non-overlapping edits can be sent in one call."
}
func (EditTool) Schema() map[string]any {
	edit := objectSchema(map[string]any{
		"oldText": stringSchema("Exact unique text to replace"),
		"newText": stringSchema("Replacement text"),
	}, []string{"oldText", "newText"})
	return objectSchema(map[string]any{
		"path":  stringSchema("Path to the file to edit"),
		"edits": map[string]any{"type": "array", "items": edit},
	}, []string{"path", "edits"})
}
func (t EditTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Path    string          `json:"path"`
		Edits   json.RawMessage `json:"edits"`
		OldText string          `json:"oldText"`
		NewText string          `json:"newText"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
		return toolError("Invalid edit input: path and edits are required")
	}
	edits := parseEditsField(args.Edits)
	if len(edits) == 0 && args.OldText != "" {
		edits = []Edit{{OldText: args.OldText, NewText: args.NewText}}
	}
	if len(edits) == 0 {
		return toolError("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	abs := ResolveInCWD(t.CWD, args.Path)
	return withFileMutationQueue(abs, func() ai.ToolResult {
		if err := ctx.Err(); err != nil {
			return toolError(err.Error())
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return toolError(fmt.Sprintf("Could not edit file: %s. %s", args.Path, err))
		}
		rawContent := string(data)
		bom, content := stripBOM(rawContent)
		ending := detectLineEnding(content)
		normalized := normalizeToLF(content)
		base, updated, err := applyEdits(normalized, edits, args.Path)
		if err != nil {
			return toolError(err.Error())
		}
		final := bom + restoreLineEndings(updated, ending)
		if err := atomicWriteFile(abs, []byte(final), fileWriteMode(abs, 0o644)); err != nil {
			return toolError(err.Error())
		}
		diff := simpleDiff(base, updated)
		details := map[string]any{
			"diff":             diff,
			"patch":            unifiedPatch(args.Path, base, updated),
			"firstChangedLine": firstChangedLine(base, updated),
		}
		return ai.ToolResult{Content: ai.TextBlocks(fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), args.Path)), Details: details}
	})
}

// parseEditsField decodes the edits argument. Some models (e.g. Opus 4.6,
// GLM-5.1) send edits as a JSON string instead of an array, so fall back to
// parsing the string, matching the TypeScript prepareEditArguments behaviour.
func parseEditsField(raw json.RawMessage) []Edit {
	if len(raw) == 0 {
		return nil
	}
	var edits []Edit
	if err := json.Unmarshal(raw, &edits); err == nil {
		return edits
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(encoded), &edits); err != nil {
		return nil
	}
	return edits
}

type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func stripBOM(content string) (string, string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", strings.TrimPrefix(content, "\uFEFF")
	}
	return "", content
}

func detectLineEnding(content string) string {
	crlf := strings.Index(content, "\r\n")
	lf := strings.Index(content, "\n")
	if lf < 0 || crlf < 0 {
		return "\n"
	}
	if crlf <= lf {
		return "\r\n"
	}
	return "\n"
}

func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func normalizeForFuzzy(text string) string {
	replacements := []struct{ old, new string }{
		{"\u2018", "'"}, {"\u2019", "'"}, {"\u201a", "'"}, {"\u201b", "'"},
		{"\u201c", `"`}, {"\u201d", `"`}, {"\u201e", `"`}, {"\u201f", `"`},
		{"\u2010", "-"}, {"\u2011", "-"}, {"\u2012", "-"}, {"\u2013", "-"}, {"\u2014", "-"}, {"\u2015", "-"}, {"\u2212", "-"},
		{"\u00a0", " "}, {"\u202f", " "}, {"\u205f", " "}, {"\u3000", " "},
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	for _, r := range replacements {
		text = strings.ReplaceAll(text, r.old, r.new)
	}
	return text
}

func applyEdits(content string, edits []Edit, path string) (string, string, error) {
	type match struct {
		index int
		end   int
		edit  Edit
		num   int
	}
	normalized := make([]Edit, len(edits))
	for i, e := range edits {
		normalized[i] = Edit{OldText: normalizeToLF(e.OldText), NewText: normalizeToLF(e.NewText)}
		if normalized[i].OldText == "" {
			return "", "", fmt.Errorf("edits[%d].oldText must not be empty in %s", i, path)
		}
	}
	base := content
	for _, e := range normalized {
		if !strings.Contains(base, e.OldText) && strings.Contains(normalizeForFuzzy(base), normalizeForFuzzy(e.OldText)) {
			base = normalizeForFuzzy(base)
			break
		}
	}
	matches := make([]match, 0, len(normalized))
	for i, e := range normalized {
		oldText := e.OldText
		if base != content {
			oldText = normalizeForFuzzy(oldText)
		}
		count := strings.Count(base, oldText)
		if count == 0 {
			if len(normalized) == 1 {
				return "", "", fmt.Errorf("could not find the exact text in %s. The old text must match exactly including all whitespace and newlines", path)
			}
			return "", "", fmt.Errorf("could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines", i, path)
		}
		if count > 1 {
			return "", "", fmt.Errorf("found %d occurrences of edits[%d] in %s. Each oldText must be unique", count, i, path)
		}
		idx := strings.Index(base, oldText)
		matches = append(matches, match{index: idx, end: idx + len(oldText), edit: e, num: i})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].index < matches[j].index })
	for i := 1; i < len(matches); i++ {
		if matches[i-1].end > matches[i].index {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions", matches[i-1].num, matches[i].num, path)
		}
	}
	updated := base
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		updated = updated[:m.index] + m.edit.NewText + updated[m.end:]
	}
	if updated == base {
		return "", "", fmt.Errorf("no changes made to %s. The replacement produced identical content", path)
	}
	return base, updated, nil
}

func simpleDiff(oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	var b strings.Builder
	maxLen := max(len(oldLines), len(newLines))
	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if i >= len(oldLines) {
			fmt.Fprintf(&b, "+%d %s\n", i+1, newLine)
		} else if i >= len(newLines) {
			fmt.Fprintf(&b, "-%d %s\n", i+1, oldLine)
		} else if oldLine != newLine {
			fmt.Fprintf(&b, "-%d %s\n+%d %s\n", i+1, oldLine, i+1, newLine)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func unifiedPatch(path, oldText, newText string) string {
	return generateUnifiedPatch(path, oldText, newText, 4)
}

func firstChangedLine(oldText, newText string) int {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	for i := 0; i < min(len(oldLines), len(newLines)); i++ {
		if oldLines[i] != newLines[i] {
			return i + 1
		}
	}
	if len(oldLines) != len(newLines) {
		return min(len(oldLines), len(newLines)) + 1
	}
	return 0
}

// generateUnifiedPatch produces a standard unified diff (with @@ hunks) between
// oldText and newText for path. It is a faithful port of the jsdiff v8
// createTwoFilesPatch used by the TypeScript edit tool, with FILE_HEADERS_ONLY
// headers and the default of 4 context lines, so the emitted `patch` detail is
// parseable by standard tools (git apply, patch) and matches upstream shape.
func generateUnifiedPatch(path, oldText, newText string, context int) string {
	if context < 0 {
		context = 0
	}
	diff := diffTokens(splitLinesWithNL(oldText), splitLinesWithNL(newText))
	// jsdiff appends an empty sentinel value to make hunk cleanup uniform.
	diff = append(diff, diffChange{})

	type hunk struct {
		oldStart, oldLines int
		newStart, newLines int
		lines              []string
	}
	contextLines := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, e := range lines {
			out[i] = " " + e
		}
		return out
	}

	var hunks []hunk
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1
	for i := 0; i < len(diff); i++ {
		current := diff[i]
		lines := current.lines
		if current.added || current.removed {
			if oldRangeStart == 0 {
				oldRangeStart = oldLine
				newRangeStart = newLine
				if i-1 >= 0 {
					prev := diff[i-1].lines
					if context > 0 {
						sliceStart := len(prev) - context
						if sliceStart < 0 {
							sliceStart = 0
						}
						curRange = contextLines(prev[sliceStart:])
					} else {
						curRange = nil
					}
					oldRangeStart -= len(curRange)
					newRangeStart -= len(curRange)
				}
			}
			for _, line := range lines {
				if current.added {
					curRange = append(curRange, "+"+line)
				} else {
					curRange = append(curRange, "-"+line)
				}
			}
			if current.added {
				newLine += len(lines)
			} else {
				oldLine += len(lines)
			}
		} else {
			if oldRangeStart != 0 {
				if len(lines) <= context*2 && i < len(diff)-2 {
					curRange = append(curRange, contextLines(lines)...)
				} else {
					contextSize := len(lines)
					if context < contextSize {
						contextSize = context
					}
					curRange = append(curRange, contextLines(lines[:contextSize])...)
					hunks = append(hunks, hunk{
						oldStart: oldRangeStart,
						oldLines: oldLine - oldRangeStart + contextSize,
						newStart: newRangeStart,
						newLines: newLine - newRangeStart + contextSize,
						lines:    curRange,
					})
					oldRangeStart, newRangeStart = 0, 0
					curRange = nil
				}
			}
			oldLine += len(lines)
			newLine += len(lines)
		}
	}

	// Strip the trailing newline from each emitted line and insert the
	// "\ No newline at end of file" marker where a line lacked one.
	for h := range hunks {
		lines := hunks[h].lines
		for i := 0; i < len(lines); i++ {
			if strings.HasSuffix(lines[i], "\n") {
				lines[i] = lines[i][:len(lines[i])-1]
			} else {
				lines = append(lines[:i+1], append([]string{"\\ No newline at end of file"}, lines[i+1:]...)...)
				i++
			}
		}
		hunks[h].lines = lines
	}

	ret := []string{"--- " + path, "+++ " + path}
	for _, h := range hunks {
		// Unified diff quirk: a zero-length side starts one lower than expected.
		if h.oldLines == 0 {
			h.oldStart--
		}
		if h.newLines == 0 {
			h.newStart--
		}
		ret = append(ret, fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.oldStart, h.oldLines, h.newStart, h.newLines))
		ret = append(ret, h.lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}

// splitLinesWithNL splits text into lines that retain their trailing newline,
// except a final line without one. It mirrors jsdiff's splitLines so empty and
// no-final-newline inputs are tokenised identically.
func splitLinesWithNL(text string) []string {
	hasTrailingNL := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	for i := range parts {
		parts[i] += "\n"
	}
	if hasTrailingNL {
		parts = parts[:len(parts)-1]
	} else {
		last := parts[len(parts)-1]
		parts[len(parts)-1] = last[:len(last)-1]
	}
	return parts
}

type diffChange struct {
	added   bool
	removed bool
	lines   []string
}

// diffTokens computes a line-level diff between a and b, grouping output as
// jsdiff does: common runs are coalesced, and within a change region all removed
// lines precede all added lines. Common prefix/suffix are trimmed before the LCS
// pass so localized edits stay cheap on large files.
func diffTokens(a, b []string) []diffChange {
	var out []diffChange
	pushCommon := func(lines []string) {
		if len(lines) == 0 {
			return
		}
		if n := len(out); n > 0 && !out[n-1].added && !out[n-1].removed {
			out[n-1].lines = append(out[n-1].lines, lines...)
			return
		}
		out = append(out, diffChange{lines: append([]string(nil), lines...)})
	}
	pushRemoved := func(lines []string) {
		if len(lines) > 0 {
			out = append(out, diffChange{removed: true, lines: lines})
		}
	}
	pushAdded := func(lines []string) {
		if len(lines) > 0 {
			out = append(out, diffChange{added: true, lines: lines})
		}
	}

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	ra, rb := a[prefix:], b[prefix:]
	suffix := 0
	for suffix < len(ra) && suffix < len(rb) && ra[len(ra)-1-suffix] == rb[len(rb)-1-suffix] {
		suffix++
	}
	ma := ra[:len(ra)-suffix]
	mb := rb[:len(rb)-suffix]

	pushCommon(a[:prefix])

	// LCS length table over the differing middle.
	la, lb := len(ma), len(mb)
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
	}
	for i := la - 1; i >= 0; i-- {
		for j := lb - 1; j >= 0; j-- {
			if ma[i] == mb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var pendRem, pendAdd []string
	flush := func() {
		pushRemoved(pendRem)
		pushAdded(pendAdd)
		pendRem, pendAdd = nil, nil
	}
	i, j := 0, 0
	for i < la && j < lb {
		switch {
		case ma[i] == mb[j]:
			flush()
			pushCommon([]string{ma[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			pendRem = append(pendRem, ma[i])
			i++
		default:
			pendAdd = append(pendAdd, mb[j])
			j++
		}
	}
	for ; i < la; i++ {
		pendRem = append(pendRem, ma[i])
	}
	for ; j < lb; j++ {
		pendAdd = append(pendAdd, mb[j])
	}
	flush()

	if suffix > 0 {
		pushCommon(ra[len(ra)-suffix:])
	}
	return out
}
