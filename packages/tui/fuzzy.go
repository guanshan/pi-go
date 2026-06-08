package tui

import (
	"sort"
	"strings"
	"unicode"
)

// FuzzyMatch is the result of FuzzyMatchString. Lower Score is better.
//
// Score is float64 to match the TS upstream (fuzzy.ts), whose `score` is a
// JS number and accumulates a fractional "later position" penalty of i*0.1
// per matched char. Using float64 preserves TS's fine-grained tie-breaking.
type FuzzyMatch struct {
	Value string
	Score float64
}

// FuzzyMatchScore reports whether pattern fuzzy-matches text and returns a
// match score (lower = better). Scoring rewards consecutive matches, word
// boundaries, and exact equality; gaps and late positions add penalty.
//
// The bool return is false when pattern is not a subsequence of text.
func FuzzyMatchScore(pattern, text string) (float64, bool) {
	if pattern == "" {
		return 0, true
	}
	pl := strings.ToLower(pattern)
	tl := strings.ToLower(text)
	if len(pl) > len(tl) {
		// Try the alphanumeric / numericalpha swap variant.
		if swapped := alphaDigitSwap(pl); swapped != "" {
			if score, ok := primaryFuzzy(swapped, tl); ok {
				return score + 5, true
			}
		}
		return 0, false
	}
	score, ok := primaryFuzzy(pl, tl)
	if ok {
		return score, true
	}
	// Fallback: try alphanum/numeralpha swap.
	if swapped := alphaDigitSwap(pl); swapped != "" {
		if score2, ok2 := primaryFuzzy(swapped, tl); ok2 {
			return score2 + 5, true
		}
	}
	return 0, false
}

// FuzzyMatchString is a convenience wrapper preserving the original Go API.
// Returns (match, ok). The match's Value field carries the original text.
func FuzzyMatchString(pattern, value string) (FuzzyMatch, bool) {
	score, ok := FuzzyMatchScore(pattern, value)
	if !ok {
		return FuzzyMatch{}, false
	}
	return FuzzyMatch{Value: value, Score: score}, true
}

func primaryFuzzy(pl, tl string) (float64, bool) {
	queryIdx := 0
	score := 0.0
	lastMatchIdx := -1
	consecutive := 0
	pr := []rune(pl)
	for i, c := range tl {
		if queryIdx >= len(pr) {
			break
		}
		if c != pr[queryIdx] {
			continue
		}
		isWordBoundary := i == 0
		if !isWordBoundary {
			prev := rune(tl[i-1])
			if isWordBoundaryChar(prev) {
				isWordBoundary = true
			}
		}
		if lastMatchIdx == i-1 {
			consecutive++
			score -= float64(consecutive * 5)
		} else {
			consecutive = 0
			if lastMatchIdx >= 0 {
				score += float64((i - lastMatchIdx - 1) * 2)
			}
		}
		if isWordBoundary {
			score -= 10
		}
		// Slight penalty for later matches, matching TS `score += i * 0.1`
		// (fractional). Earlier Go used integer `i / 10`, a step function that
		// is 0 for i<10 and coarsened the positional tie-breaker by 10x.
		score += float64(i) * 0.1
		lastMatchIdx = i
		queryIdx++
	}
	if queryIdx < len(pr) {
		return 0, false
	}
	if pl == tl {
		score -= 100
	}
	return score, true
}

func isWordBoundaryChar(r rune) bool {
	switch r {
	case ' ', '\t', '-', '_', '.', '/', ':':
		return true
	}
	return unicode.IsSpace(r)
}

func alphaDigitSwap(s string) string {
	// "ab12" → "12ab", "12ab" → "ab12", else "".
	letters := []rune{}
	digits := []rune{}
	mode := 0 // 0 = unknown, 1 = letters first, 2 = digits first
	for i, r := range s {
		isLetter := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit {
			return ""
		}
		if i == 0 {
			if isLetter {
				mode = 1
			} else {
				mode = 2
			}
		}
		switch mode {
		case 1:
			if len(digits) > 0 && isLetter {
				return ""
			}
			if isLetter {
				letters = append(letters, r)
			} else {
				digits = append(digits, r)
			}
		case 2:
			if len(letters) > 0 && isDigit {
				return ""
			}
			if isDigit {
				digits = append(digits, r)
			} else {
				letters = append(letters, r)
			}
		}
	}
	if len(letters) == 0 || len(digits) == 0 {
		return ""
	}
	if mode == 1 {
		return string(digits) + string(letters)
	}
	return string(letters) + string(digits)
}

// FuzzyFilter returns the items that fuzzy-match the query, sorted by score.
// Multi-token queries (whitespace-separated) require all tokens to match.
//
// getText extracts the text to match against from each item.
func FuzzyFilter[T any](items []T, query string, getText func(T) string) []T {
	q := strings.TrimSpace(query)
	if q == "" {
		out := make([]T, len(items))
		copy(out, items)
		return out
	}
	tokens := strings.Fields(q)
	type scored struct {
		item  T
		score float64
	}
	var matched []scored
	for _, item := range items {
		text := getText(item)
		total := 0.0
		ok := true
		for _, tok := range tokens {
			if score, hit := FuzzyMatchScore(tok, text); hit {
				total += score
			} else {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, scored{item: item, score: total})
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].score < matched[j].score })
	out := make([]T, len(matched))
	for i, s := range matched {
		out[i] = s.item
	}
	return out
}
