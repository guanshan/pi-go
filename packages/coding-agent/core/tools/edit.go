package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/guanshan/pi-go/packages/ai"
	"golang.org/x/text/unicode/norm"
)

func (EditTool) Name() string { return "edit" }
func (EditTool) Description() string {
	// Byte-exact with edit.ts:295.
	return "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes."
}
func (EditTool) Schema() map[string]any {
	edit := strictObjectSchema(map[string]any{
		"oldText": stringSchema("Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."),
		"newText": stringSchema("Replacement text for this targeted edit."),
	}, []string{"oldText", "newText"})
	return strictObjectSchema(map[string]any{
		"path": stringSchema("Path to the file to edit (relative or absolute)"),
		"edits": map[string]any{
			"type":        "array",
			"items":       edit,
			"description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
		},
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
	abs := ResolveToolPath(t.CWD, args.Path)
	return withFileMutationQueue(abs, func() ai.ToolResult {
		if err := ctx.Err(); err != nil {
			return toolError(err.Error())
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return toolError(fmt.Sprintf("Could not edit file: %s. %s.", args.Path, editAccessErrorMessage(err)))
		}
		// Write-permission preflight: a readable-but-not-writable file (e.g. 0o444)
		// would otherwise be silently overwritten via the temp-file replace, since
		// atomicWriteFile writes a sibling temp in the (writable) directory. TS
		// reports EACCES for such files (edit.ts:323-331), so probe writability and
		// surface the same message. This is racy vs the actual write (TOCTOU), but
		// matches TS which has the same access-then-write race.
		if perr := checkWritable(abs); perr != nil {
			return toolError(fmt.Sprintf("Could not edit file: %s. %s.", args.Path, editAccessErrorMessage(perr)))
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
		// Mirror edit.ts:350,359: both the display diff and firstChangedLine derive
		// from generateDiffString's LCS line diff (not a naive positional scan).
		diff, changedLine := generateDiffString(base, updated, 4)
		details := map[string]any{
			"diff":             diff,
			"patch":            unifiedPatch(args.Path, base, updated),
			"firstChangedLine": changedLine,
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

// normalizeForFuzzy mirrors TS normalizeForFuzzyMatch (edit-diff.ts:34-55).
// Applies progressive transformations in the same order:
//   - Unicode NFKC normalization (TS .normalize("NFKC"))
//   - Strip trailing whitespace from each line
//   - Smart quotes -> ASCII equivalents
//   - Unicode dashes/hyphens -> ASCII hyphen
//   - Special Unicode spaces -> regular space
func normalizeForFuzzy(text string) string {
	replacements := []struct{ old, new string }{
		// Smart single quotes -> '
		{"\u2018", "'"}, {"\u2019", "'"}, {"\u201a", "'"}, {"\u201b", "'"},
		// Smart double quotes -> "
		{"\u201c", `"`}, {"\u201d", `"`}, {"\u201e", `"`}, {"\u201f", `"`},
		// Various dashes/hyphens -> -
		{"\u2010", "-"}, {"\u2011", "-"}, {"\u2012", "-"}, {"\u2013", "-"}, {"\u2014", "-"}, {"\u2015", "-"}, {"\u2212", "-"},
		// Special spaces -> regular space
		// U+00A0 NBSP, U+2002-U+200A various spaces, U+202F narrow NBSP,
		// U+205F medium math space, U+3000 ideographic space
		{"\u00a0", " "},
		{"\u2002", " "}, {"\u2003", " "}, {"\u2004", " "}, {"\u2005", " "}, {"\u2006", " "},
		{"\u2007", " "}, {"\u2008", " "}, {"\u2009", " "}, {"\u200a", " "},
		{"\u202f", " "}, {"\u205f", " "}, {"\u3000", " "},
	}
	text = norm.NFKC.String(text)
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

// countFuzzyOccurrences mirrors TS countOccurrences (edit-diff.ts:141-145): it
// always counts in fuzzy-normalized space, regardless of whether the surrounding
// match ran against exact or normalized content.
func countFuzzyOccurrences(content, oldText string) int {
	return strings.Count(normalizeForFuzzy(content), normalizeForFuzzy(oldText))
}

// fuzzyFindText mirrors TS fuzzyFindText (edit-diff.ts:96-134): it tries an exact
// substring match first, then falls back to matching in fuzzy-normalized space.
// It returns the match index (or -1), the matched length, and whether fuzzy
// matching was used. Indices/lengths are relative to content when the match is
// exact and relative to normalizeForFuzzy(content) when fuzzy matching is used.
func fuzzyFindText(content, oldText string) (index, matchLength int, usedFuzzy bool) {
	if exact := strings.Index(content, oldText); exact != -1 {
		return exact, len(oldText), false
	}
	fuzzyContent := normalizeForFuzzy(content)
	fuzzyOldText := normalizeForFuzzy(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return -1, 0, false
	}
	return fuzzyIndex, len(fuzzyOldText), true
}

// checkWritable probes whether the target file can be opened for writing,
// returning the OS error (e.g. EACCES on a 0o444 file) when it cannot. It opens
// O_WRONLY without truncation and closes immediately, so it does not mutate the
// file. A non-existent file yields no error here (creation is handled by the
// caller); only an existing, unwritable file fails.
func checkWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return f.Close()
}

// editAccessErrorMessage mirrors TS edit.ts:328-330: when the underlying error
// carries an OS error code, surface it as "Error code: <CODE>" (Node-style),
// otherwise fall back to the error string.
func editAccessErrorMessage(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if code := nodeErrorCode(errno); code != "" {
			return "Error code: " + code
		}
	}
	return err.Error()
}

// nodeErrorCode maps the common POSIX errnos Go returns from filesystem calls to
// the Node.js error-code strings (e.g. ENOENT, EACCES) that the TypeScript edit
// tool reports, so error wording stays identical across runtimes.
func nodeErrorCode(errno syscall.Errno) string {
	switch errno {
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EISDIR:
		return "EISDIR"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.EPERM:
		return "EPERM"
	case syscall.ELOOP:
		return "ELOOP"
	case syscall.ENAMETOOLONG:
		return "ENAMETOOLONG"
	}
	return ""
}

//nolint:staticcheck // Edit tool diagnostics intentionally mirror TS user-facing wording.
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
	}
	for i := range normalized {
		if normalized[i].OldText == "" {
			if len(normalized) == 1 {
				return "", "", fmt.Errorf("oldText must not be empty in %s.", path)
			}
			return "", "", fmt.Errorf("edits[%d].oldText must not be empty in %s.", i, path)
		}
	}
	// If any edit only matches after fuzzy normalization, switch the whole
	// base to fuzzy-normalized space (mirrors TS applyEditsToNormalizedContent
	// using normalizeForFuzzyMatch when initialMatches.some(usedFuzzyMatch)).
	base := content
	for _, e := range normalized {
		if _, _, used := fuzzyFindText(base, e.OldText); used {
			base = normalizeForFuzzy(content)
			break
		}
	}
	matches := make([]match, 0, len(normalized))
	for i, e := range normalized {
		idx, matchLen, _ := fuzzyFindText(base, e.OldText)
		if idx < 0 {
			if len(normalized) == 1 {
				return "", "", fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
			}
			return "", "", fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", i, path)
		}
		// Uniqueness is always counted in fuzzy-normalized space (TS countOccurrences).
		count := countFuzzyOccurrences(base, e.OldText)
		if count > 1 {
			if len(normalized) == 1 {
				return "", "", fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", count, path)
			}
			return "", "", fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", count, i, path)
		}
		matches = append(matches, match{index: idx, end: idx + matchLen, edit: e, num: i})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].index < matches[j].index })
	for i := 1; i < len(matches); i++ {
		if matches[i-1].end > matches[i].index {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.", matches[i-1].num, matches[i].num, path)
		}
	}
	updated := base
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		updated = updated[:m.index] + m.edit.NewText + updated[m.end:]
	}
	if updated == base {
		if len(normalized) == 1 {
			return "", "", fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
		}
		return "", "", fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
	}
	return base, updated, nil
}

func unifiedPatch(path, oldText, newText string) string {
	return generateUnifiedPatch(path, oldText, newText, 4)
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
