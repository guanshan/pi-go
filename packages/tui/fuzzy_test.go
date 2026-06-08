package tui

import "testing"

func TestFuzzyMatchScore(t *testing.T) {
	cases := []struct {
		pattern string
		text    string
		match   bool
	}{
		{"", "anything", true},
		{"abc", "abcdef", true},
		{"abc", "axbxcx", true},
		{"xyz", "abc", false},
		{"abc", "ab", false},
	}
	for _, c := range cases {
		_, ok := FuzzyMatchScore(c.pattern, c.text)
		if ok != c.match {
			t.Errorf("FuzzyMatchScore(%q, %q) ok=%v, want %v", c.pattern, c.text, ok, c.match)
		}
	}
}

func TestFuzzyScoreBoundaryBoost(t *testing.T) {
	// "fmt" at word boundary should outscore (= lower) "fmt" mid-word.
	a, _ := FuzzyMatchScore("fmt", "fmt-printer")
	b, _ := FuzzyMatchScore("fmt", "informationmt")
	if a >= b {
		t.Errorf("boundary boost: a=%g b=%g", a, b)
	}
}

func TestFuzzyMatchExactScoresBest(t *testing.T) {
	exact, _ := FuzzyMatchScore("foo", "foo")
	prefix, _ := FuzzyMatchScore("foo", "foobar")
	if exact >= prefix {
		t.Errorf("exact %g should beat prefix %g", exact, prefix)
	}
}

// TestFuzzyLaterPositionPenaltyFractional locks in TS parity for the positional
// penalty `score += i * 0.1` (fuzzy.ts:52). The Go port previously used integer
// `i / 10`, which is 0 for every position < 10 — erasing the tie-breaker for
// short strings. The penalty must be fractional and strictly increasing with
// position, so a single matched char at a later position scores strictly higher
// (worse) than the same char earlier, even within the first 10 positions.
func TestFuzzyLaterPositionPenaltyFractional(t *testing.T) {
	// Single char "x"; in "axxxxxxx" the first 'x' is at index 1, in
	// "aaaaaax" it is at index 6. No boundary/consecutive/gap effects differ
	// here (both are single, non-boundary, non-consecutive matches), so the
	// ONLY differentiator is the i*0.1 positional penalty. With integer i/10
	// both would be 0 and tie; with TS's fractional penalty the earlier match
	// must score strictly lower (better).
	early, ok1 := FuzzyMatchScore("x", "axyyyyyy")
	late, ok2 := FuzzyMatchScore("x", "aaaaaax")
	if !ok1 || !ok2 {
		t.Fatalf("expected both to match: early=%v late=%v", ok1, ok2)
	}
	if !(early < late) {
		t.Fatalf("fractional positional penalty lost: early=%g should be < late=%g", early, late)
	}
	// The exact fractional values must match TS arithmetic: position 1 -> 0.1,
	// position 6 -> 0.6 (no other components apply for a single non-boundary
	// mid-word char match). Compare with an epsilon since IEEE-754 0.1 sums are
	// not exactly representable (TS would compute the identical float64 value).
	const eps = 1e-9
	if d := early - 0.1; d < -eps || d > eps {
		t.Fatalf("position-1 single-char score: got %g want 0.1", early)
	}
	if d := late - 0.6; d < -eps || d > eps {
		t.Fatalf("position-6 single-char score: got %g want 0.6", late)
	}
}

func TestFuzzyFilterMultiToken(t *testing.T) {
	items := []string{
		"cmd-line",
		"web-server",
		"web-cli",
		"server-status",
	}
	got := FuzzyFilter(items, "web cli", func(s string) string { return s })
	if len(got) != 1 || got[0] != "web-cli" {
		t.Errorf("multi-token filter: %#v", got)
	}
}

func TestFuzzyMatchString(t *testing.T) {
	m, ok := FuzzyMatchString("abc", "axbxcx")
	if !ok {
		t.Fatal("ok")
	}
	if m.Value != "axbxcx" {
		t.Errorf("value: %q", m.Value)
	}
}
