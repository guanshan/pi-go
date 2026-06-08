package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// generateDiffString is a faithful port of edit-diff.ts generateDiffString: it
// produces a display-oriented diff with right-padded line numbers, leading
// context lines prefixed by a space, "-NN"/"+NN" markers for removals/additions,
// and " ... " skip markers around large unchanged runs. It returns the diff text
// and the first changed line number (in the new file), or 0 when nothing changed
// (TS returns undefined; the edit tool only consults the diff string).
func generateDiffString(oldContent, newContent string, contextLines int) (string, int) {
	// Mirror Diff.diffLines: tokenize into lines that retain their trailing
	// newline (jsdiff drops the empty token after a final newline). diffTokens is
	// the shared LCS line-diff used by the unified-patch path.
	parts := diffTokens(splitLinesWithNL(oldContent), splitLinesWithNL(newContent))
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := max(len(oldLines), len(newLines))
	lineNumWidth := len(strconv.Itoa(maxLineNum))

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	firstChangedLine := 0

	padNum := func(n int) string {
		return fmt.Sprintf("%*s", lineNumWidth, strconv.Itoa(n))
	}
	padBlank := func() string {
		return strings.Repeat(" ", lineNumWidth)
	}

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		// jsdiff part.value is the concatenation of newline-bearing line tokens;
		// TS computes raw = part.value.split("\n") then pops the trailing empty.
		// Equivalently, strip the trailing "\n" from each token: each token maps to
		// exactly one display line.
		raw := make([]string, len(part.lines))
		for k, line := range part.lines {
			raw[k] = strings.TrimSuffix(line, "\n")
		}

		if part.added || part.removed {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
			}
			for _, line := range raw {
				if part.added {
					output = append(output, fmt.Sprintf("+%s %s", padNum(newLineNum), line))
					newLineNum++
				} else {
					output = append(output, fmt.Sprintf("-%s %s", padNum(oldLineNum), line))
					oldLineNum++
				}
			}
			lastWasChange = true
			continue
		}

		nextPartIsChange := i < len(parts)-1 && (parts[i+1].added || parts[i+1].removed)
		hasLeadingChange := lastWasChange
		hasTrailingChange := nextPartIsChange

		switch {
		case hasLeadingChange && hasTrailingChange:
			if len(raw) <= contextLines*2 {
				for _, line := range raw {
					output = append(output, fmt.Sprintf(" %s %s", padNum(oldLineNum), line))
					oldLineNum++
					newLineNum++
				}
			} else {
				leadingLines := raw[:contextLines]
				trailingLines := raw[len(raw)-contextLines:]
				skippedLines := len(raw) - len(leadingLines) - len(trailingLines)

				for _, line := range leadingLines {
					output = append(output, fmt.Sprintf(" %s %s", padNum(oldLineNum), line))
					oldLineNum++
					newLineNum++
				}

				output = append(output, fmt.Sprintf(" %s ...", padBlank()))
				oldLineNum += skippedLines
				newLineNum += skippedLines

				for _, line := range trailingLines {
					output = append(output, fmt.Sprintf(" %s %s", padNum(oldLineNum), line))
					oldLineNum++
					newLineNum++
				}
			}
		case hasLeadingChange:
			// raw.slice(0, contextLines): JS clamps the end to the array length.
			n := contextLines
			if n > len(raw) {
				n = len(raw)
			}
			shownLines := raw[:n]
			skippedLines := len(raw) - len(shownLines)

			for _, line := range shownLines {
				output = append(output, fmt.Sprintf(" %s %s", padNum(oldLineNum), line))
				oldLineNum++
				newLineNum++
			}

			if skippedLines > 0 {
				output = append(output, fmt.Sprintf(" %s ...", padBlank()))
				oldLineNum += skippedLines
				newLineNum += skippedLines
			}
		case hasTrailingChange:
			skippedLines := max(0, len(raw)-contextLines)
			if skippedLines > 0 {
				output = append(output, fmt.Sprintf(" %s ...", padBlank()))
				oldLineNum += skippedLines
				newLineNum += skippedLines
			}

			for _, line := range raw[skippedLines:] {
				output = append(output, fmt.Sprintf(" %s %s", padNum(oldLineNum), line))
				oldLineNum++
				newLineNum++
			}
		default:
			oldLineNum += len(raw)
			newLineNum += len(raw)
		}

		lastWasChange = false
	}

	return strings.Join(output, "\n"), firstChangedLine
}
