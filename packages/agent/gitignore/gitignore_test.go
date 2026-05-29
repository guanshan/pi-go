package gitignore

import "testing"

func TestMatcherBasenameAndAnchored(t *testing.T) {
	m := New()
	for _, line := range []string{"*.log", "build/", "/root-only.txt", "node_modules"} {
		if p := PrefixPattern(line, ""); p != "" {
			m.Add(p)
		}
	}
	cases := []struct {
		path string
		want bool
	}{
		{"a.log", true},
		{"sub/dir/b.log", true},      // basename pattern matches at any depth
		{"build/", true},             // directory-only matches the dir
		{"build/out.o", true},        // build/ ignores the directory and its contents
		{"root-only.txt", true},      // anchored at root
		{"node_modules/", true},      // bare name matches dir
		{"src/node_modules/x", true}, // bare name matches segment at any depth
		{"keep.txt", false},
	}
	for _, c := range cases {
		if got := m.Ignores(c.path); got != c.want {
			t.Errorf("Ignores(%q)=%v want %v", c.path, got, c.want)
		}
	}
}

func TestMatcherNegation(t *testing.T) {
	m := New()
	m.Add(PrefixPattern("*.tmp", ""))
	m.Add(PrefixPattern("!keep.tmp", ""))
	if !m.Ignores("a.tmp") {
		t.Error("a.tmp should be ignored")
	}
	if m.Ignores("keep.tmp") {
		t.Error("keep.tmp should be re-included by negation")
	}
}

func TestPrefixPatternNesting(t *testing.T) {
	if got := PrefixPattern("secret.txt", "sub/"); got != "sub/secret.txt" {
		t.Errorf("prefixed=%q", got)
	}
	if got := PrefixPattern("!secret.txt", "sub/"); got != "!sub/secret.txt" {
		t.Errorf("negated prefixed=%q", got)
	}
	if got := PrefixPattern("# comment", ""); got != "" {
		t.Errorf("comment should be empty, got %q", got)
	}
	if got := PrefixPattern("  ", ""); got != "" {
		t.Errorf("blank should be empty, got %q", got)
	}
	// A nested rule only applies under its directory.
	m := New()
	m.Add(PrefixPattern("secret.txt", "sub/"))
	if m.Ignores("secret.txt") {
		t.Error("top-level secret.txt should not match a sub/ nested rule")
	}
	if !m.Ignores("sub/secret.txt") {
		t.Error("sub/secret.txt should match the nested rule")
	}
}
