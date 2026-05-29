// Width-aware string utilities (visible width, truncation, wrapping, ANSI
// slicing) plus a few classification helpers used across the package.

package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// THAI_LAO_AM characters that confuse some terminals when emitted directly
// (https://github.com/earendil-works/pi/issues/...).
const (
	thaiSaraAmRune = 'ำ'
	laoSaraAmRune  = 'ໍ'
)

func isThaiOrLaoAm(s string) bool {
	for _, r := range s {
		if r == thaiSaraAmRune || r == laoSaraAmRune {
			return true
		}
	}
	return false
}

// NormalizeTerminalOutput rewrites a few combining-mark sequences that some
// terminals render incorrectly (Thai/Lao SARA AM). Mirrors the upstream TS
// utility of the same name.
func NormalizeTerminalOutput(s string) string {
	if !isThaiOrLaoAm(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case thaiSaraAmRune:
			b.WriteString("ํา")
		case laoSaraAmRune:
			b.WriteString("ໍາ")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// VisibleWidth returns the visible width (in terminal cells) of a string,
// accounting for ANSI escape sequences (which contribute 0), tabs (4 cells),
// and grapheme cluster width (East-Asian + emoji aware).
func VisibleWidth(s string) int {
	if s == "" {
		return 0
	}
	width := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			if length := ansiSequenceLength(s, i); length > 0 {
				i += length
				continue
			}
			i++
			continue
		}
		if s[i] == '\t' {
			width += 4
			i++
			continue
		}
		// Find the next ANSI/control boundary, then split the run into
		// grapheme clusters and sum widths.
		end := i
		for end < len(s) && s[end] != '\x1b' && s[end] != '\t' {
			end++
		}
		segment := s[i:end]
		gr := uniseg.NewGraphemes(segment)
		for gr.Next() {
			cluster := gr.Str()
			width += clusterWidth(cluster)
		}
		i = end
	}
	return width
}

// clusterWidth returns the cell width of a single grapheme cluster. Control
// characters (other than tab, handled by callers) contribute 0.
func clusterWidth(cluster string) int {
	w := uniseg.StringWidth(cluster)
	if w > 0 {
		return w
	}
	// Treat unknown / control clusters as zero-width.
	for _, r := range cluster {
		if unicode.IsControl(r) {
			return 0
		}
	}
	return w
}

// TruncateToWidth truncates s to the given visible width, appending suffix
// when truncation actually occurs. Width <= 0 returns "". The suffix is only
// appended when the input would otherwise overflow.
func TruncateToWidth(s string, width int, suffix string) string {
	if width <= 0 {
		return ""
	}
	current := VisibleWidth(s)
	if current <= width {
		return s
	}
	suffixWidth := VisibleWidth(suffix)
	target := width - suffixWidth
	if target < 0 {
		target = width
		suffix = ""
	}
	return sliceWithWidth(s, 0, target, true).text + suffix
}

// WrapTextWithANSI breaks an ANSI-tagged text into lines no wider than width
// cells, splitting on whitespace boundaries when possible and falling back to
// hard wraps for words longer than the available width. Empty input returns
// [""].
func WrapTextWithANSI(s string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if s == "" {
		return []string{""}
	}

	var lines []string
	var line strings.Builder
	lineWidth := 0

	flushLine := func() {
		// Strip trailing whitespace before pushing — keeps wrap output free
		// of dangling spaces at line ends.
		out := strings.TrimRight(line.String(), " \t")
		lines = append(lines, out)
		line.Reset()
		lineWidth = 0
	}

	// Split on whitespace runs, preserving ANSI codes attached to each word.
	words := splitANSIWords(s)
	for _, w := range words {
		ww := VisibleWidth(w.text)
		if w.isWhitespace {
			// Whitespace only adds to line if there is content.
			if lineWidth == 0 {
				continue
			}
			if lineWidth+ww > width {
				flushLine()
				continue
			}
			line.WriteString(w.text)
			lineWidth += ww
			continue
		}
		// Word longer than width — hard-wrap.
		if ww > width {
			if lineWidth > 0 {
				flushLine()
			}
			remaining := w.text
			for VisibleWidth(remaining) > width {
				piece := sliceWithWidth(remaining, 0, width, true)
				lines = append(lines, piece.text)
				remaining = sliceFrom(remaining, piece.width)
			}
			if remaining != "" {
				line.WriteString(remaining)
				lineWidth = VisibleWidth(remaining)
			}
			continue
		}
		if lineWidth+ww > width {
			flushLine()
		}
		line.WriteString(w.text)
		lineWidth += ww
	}
	if lineWidth > 0 || len(lines) == 0 {
		flushLine()
	}
	return lines
}

// ansiWord represents one unit of WrapTextWithANSI input: either a run of
// non-whitespace text (which may include ANSI codes) or a run of whitespace.
type ansiWord struct {
	text         string
	isWhitespace bool
}

func splitANSIWords(s string) []ansiWord {
	var out []ansiWord
	var cur strings.Builder
	curIsSpace := false
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, ansiWord{text: cur.String(), isWhitespace: curIsSpace})
		cur.Reset()
	}
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			length := ansiSequenceLength(s, i)
			if length <= 0 {
				length = 1
			}
			cur.WriteString(s[i : i+length])
			i += length
			continue
		}
		r, size := decodeRune(s, i)
		isSpace := unicode.IsSpace(r)
		if cur.Len() == 0 {
			curIsSpace = isSpace
		} else if isSpace != curIsSpace {
			flush()
			curIsSpace = isSpace
		}
		cur.WriteString(s[i : i+size])
		i += size
	}
	flush()
	return out
}

// sliceFrom returns the remainder of s after the first `width` visible cells.
// ANSI codes encountered before the cut are preserved in the result.
func sliceFrom(s string, width int) string {
	total := VisibleWidth(s)
	if width >= total {
		return ""
	}
	// Reuse sliceWithWidth by asking for the tail.
	return sliceWithWidthRange(s, width, total).text
}

// SliceByColumn returns the substring of line covering the visible column
// range [startCol, startCol+length). When strict is true, wide characters
// straddling the boundary are excluded.
func SliceByColumn(line string, startCol, length int, strict bool) string {
	return sliceWithWidth(line, startCol, length, strict).text
}

type sliceResult struct {
	text  string
	width int
}

func sliceWithWidth(line string, startCol, length int, strict bool) sliceResult {
	if length <= 0 {
		return sliceResult{}
	}
	return sliceWithWidthRange(line, startCol, startCol+length, strict)
}

// SliceWithWidth is the public form of sliceWithWidth: returns the slice text
// plus its measured visible width.
func SliceWithWidth(line string, startCol, length int, strict bool) (string, int) {
	r := sliceWithWidth(line, startCol, length, strict)
	return r.text, r.width
}

func sliceWithWidthRange(line string, startCol, endCol int, strict ...bool) sliceResult {
	strictBoundary := false
	if len(strict) > 0 {
		strictBoundary = strict[0]
	}
	if endCol <= startCol {
		return sliceResult{}
	}
	var b strings.Builder
	resultWidth := 0
	currentCol := 0
	pendingAnsi := strings.Builder{}
	i := 0
	for i < len(line) {
		if line[i] == '\x1b' {
			length := ansiSequenceLength(line, i)
			if length <= 0 {
				length = 1
			}
			if currentCol >= startCol && currentCol < endCol {
				b.WriteString(line[i : i+length])
			} else if currentCol < startCol {
				pendingAnsi.WriteString(line[i : i+length])
			}
			i += length
			continue
		}
		// Process a non-ANSI run.
		end := i
		for end < len(line) && line[end] != '\x1b' {
			end++
		}
		segment := line[i:end]
		gr := uniseg.NewGraphemes(segment)
		for gr.Next() {
			cluster := gr.Str()
			w := clusterWidth(cluster)
			if w == 0 {
				// Zero-width clusters (combining marks) are attached to the
				// previous visible cell.
				if currentCol > startCol && currentCol <= endCol {
					b.WriteString(cluster)
				}
				continue
			}
			inRange := currentCol >= startCol && currentCol < endCol
			fits := !strictBoundary || currentCol+w <= endCol
			if inRange && fits {
				if pendingAnsi.Len() > 0 {
					b.WriteString(pendingAnsi.String())
					pendingAnsi.Reset()
				}
				b.WriteString(cluster)
				resultWidth += w
			}
			currentCol += w
			if currentCol >= endCol {
				break
			}
		}
		if currentCol >= endCol {
			break
		}
		i = end
	}
	return sliceResult{text: b.String(), width: resultWidth}
}

// ApplyBackgroundToLine pads line to the given visible width, then runs the
// padded line through bgFn (typically a lipgloss/ANSI background applier).
func ApplyBackgroundToLine(line string, width int, bgFn func(string) string) string {
	if bgFn == nil {
		return line
	}
	visLen := VisibleWidth(line)
	pad := width - visLen
	if pad < 0 {
		pad = 0
	}
	padded := line + strings.Repeat(" ", pad)
	return bgFn(padded)
}

// IsWhitespaceChar reports whether the cluster represents whitespace.
// Single-rune ASCII whitespace and Unicode whitespace (per unicode.IsSpace)
// are both considered whitespace.
func IsWhitespaceChar(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// PunctuationCharset is the set of ASCII punctuation characters that act as
// word boundaries during navigation. Mirrors PUNCTUATION_REGEX upstream.
const PunctuationCharset = "(){}[]<>.,;:'\"!?+-=*/\\|&%^$#@~`"

// PunctuationRegex is the equivalent regular-expression character class.
// Useful when callers want to use a regex (e.g. inside fuzzy boundary
// scoring). Provided for API parity with upstream's PUNCTUATION_REGEX.
const PunctuationRegex = `[(){}[\]<>.,;:'"!?+\-=*/\\|&%^$#@~` + "`" + `]`

// IsPunctuationChar reports whether the cluster is a punctuation mark.
func IsPunctuationChar(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(PunctuationCharset, r) {
			return false
		}
	}
	return true
}

// SegmentExtraction is the result of ExtractSegments — the "before" and
// "after" parts of a line surrounding an overlay region.
type SegmentExtraction struct {
	Before      string
	BeforeWidth int
	After       string
	AfterWidth  int
}

// ExtractSegments splits a line into the substring covering visible columns
// [0, beforeEnd) and the substring covering [afterStart, afterStart+afterLen).
// ANSI codes that affect the "after" region are propagated so styling
// continues correctly even when an overlay punches a hole in the middle.
//
// strictAfter, when true, excludes wide characters straddling the right
// boundary of the "after" region.
func ExtractSegments(line string, beforeEnd, afterStart, afterLen int, strictAfter bool) SegmentExtraction {
	before := sliceWithWidth(line, 0, beforeEnd, false)
	after := sliceWithWidth(line, afterStart, afterLen, strictAfter)
	return SegmentExtraction{
		Before:      before.text,
		BeforeWidth: before.width,
		After:       after.text,
		AfterWidth:  after.width,
	}
}

// decodeRune is a tiny helper around utf8.DecodeRuneInString that returns the
// rune and its byte size at offset i.
func decodeRune(s string, i int) (rune, int) {
	if i >= len(s) {
		return 0, 0
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	return r, size
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
