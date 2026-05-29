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
		t.Errorf("boundary boost: a=%d b=%d", a, b)
	}
}

func TestFuzzyMatchExactScoresBest(t *testing.T) {
	exact, _ := FuzzyMatchScore("foo", "foo")
	prefix, _ := FuzzyMatchScore("foo", "foobar")
	if exact >= prefix {
		t.Errorf("exact %d should beat prefix %d", exact, prefix)
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
