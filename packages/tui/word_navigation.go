package tui

import "github.com/rivo/uniseg"

// WordNavigationOptions tweaks word navigation. Both fields are optional.
//
//   - Segment, when provided, replaces the default grapheme-cluster
//     segmenter. It receives a substring of the text (the prefix or
//     suffix being walked) and must return the cluster boundaries in
//     order.
//   - IsAtomic identifies clusters that should be treated as a single
//     atomic unit (e.g. paste markers, emoji ZWJ sequences). The
//     navigator skips/keeps such clusters as one step regardless of
//     surrounding word/punctuation context.
type WordNavigationOptions struct {
	Segment  func(text string) []string
	IsAtomic func(cluster string) bool
}

// FindWordBackward returns the rune-index in text where the cursor should
// land after moving one "word" backwards from cursor. Whitespace is skipped
// first, then the cursor is placed at the start of the previous word- or
// punctuation-run.
//
// `cursor` is interpreted as a byte index into text and the return value is a
// byte index. Both must align with grapheme cluster boundaries; otherwise
// the function returns the nearest aligned boundary at or below `cursor`.
//
// Mirrors the upstream TS findWordBackward.
func FindWordBackward(text string, cursor int, opts ...WordNavigationOptions) int {
	if cursor <= 0 {
		return 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}
	prefix := text[:cursor]
	o := joinWordOpts(opts)
	clusters := segmentWord(prefix, o)
	pos := cursor

	// Skip trailing whitespace, but never break across an atomic cluster.
	for len(clusters) > 0 {
		last := clusters[len(clusters)-1]
		if o.IsAtomic != nil && o.IsAtomic(last) {
			break
		}
		if !IsWhitespaceChar(last) {
			break
		}
		pos -= len(last)
		clusters = clusters[:len(clusters)-1]
	}
	if len(clusters) == 0 {
		return pos
	}

	last := clusters[len(clusters)-1]
	if o.IsAtomic != nil && o.IsAtomic(last) {
		return pos - len(last)
	}
	if IsPunctuationChar(last) {
		for len(clusters) > 0 {
			c := clusters[len(clusters)-1]
			if o.IsAtomic != nil && o.IsAtomic(c) {
				break
			}
			if !IsPunctuationChar(c) {
				break
			}
			pos -= len(c)
			clusters = clusters[:len(clusters)-1]
		}
		return pos
	}
	for len(clusters) > 0 {
		c := clusters[len(clusters)-1]
		if o.IsAtomic != nil && o.IsAtomic(c) {
			break
		}
		if IsWhitespaceChar(c) || IsPunctuationChar(c) {
			break
		}
		pos -= len(c)
		clusters = clusters[:len(clusters)-1]
	}
	return pos
}

// FindWordForward returns the byte index where the cursor should land after
// moving one word forwards from cursor.
func FindWordForward(text string, cursor int, opts ...WordNavigationOptions) int {
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(text) {
		return len(text)
	}
	suffix := text[cursor:]
	o := joinWordOpts(opts)
	clusters := segmentWord(suffix, o)
	pos := cursor
	idx := 0

	for idx < len(clusters) {
		c := clusters[idx]
		if o.IsAtomic != nil && o.IsAtomic(c) {
			break
		}
		if !IsWhitespaceChar(c) {
			break
		}
		pos += len(c)
		idx++
	}
	if idx >= len(clusters) {
		return pos
	}
	first := clusters[idx]
	if o.IsAtomic != nil && o.IsAtomic(first) {
		return pos + len(first)
	}
	if IsPunctuationChar(first) {
		for idx < len(clusters) {
			c := clusters[idx]
			if o.IsAtomic != nil && o.IsAtomic(c) {
				break
			}
			if !IsPunctuationChar(c) {
				break
			}
			pos += len(c)
			idx++
		}
		return pos
	}
	for idx < len(clusters) {
		c := clusters[idx]
		if o.IsAtomic != nil && o.IsAtomic(c) {
			break
		}
		if IsWhitespaceChar(c) || IsPunctuationChar(c) {
			break
		}
		pos += len(c)
		idx++
	}
	return pos
}

func joinWordOpts(opts []WordNavigationOptions) WordNavigationOptions {
	if len(opts) == 0 {
		return WordNavigationOptions{}
	}
	return opts[0]
}

func segmentWord(s string, o WordNavigationOptions) []string {
	if s == "" {
		return nil
	}
	if o.Segment != nil {
		return o.Segment(s)
	}
	return collectClusters(s)
}

func collectClusters(s string) []string {
	if s == "" {
		return nil
	}
	gr := uniseg.NewGraphemes(s)
	var out []string
	for gr.Next() {
		out = append(out, gr.Str())
	}
	return out
}
