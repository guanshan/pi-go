package gitignore

import (
	"path"
	"strings"
)

type pattern struct {
	pattern string
	negated bool
	dirOnly bool
}

// Matcher accumulates ignore patterns and reports whether a path is ignored. The
// last matching pattern wins, so a later negation (!) re-includes a path.
type Matcher struct {
	patterns []pattern
}

// New returns an empty Matcher.
func New() Matcher {
	return Matcher{}
}

// Add registers a single already-prefixed ignore pattern (the line is expected to
// have passed through PrefixPattern, which strips comments and applies directory
// prefixes). Blank patterns are ignored.
func (m *Matcher) Add(p string) {
	negated := strings.HasPrefix(p, "!")
	if negated {
		p = strings.TrimPrefix(p, "!")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	dirOnly := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	m.patterns = append(m.patterns, pattern{pattern: p, negated: negated, dirOnly: dirOnly})
}

// Ignores reports whether relPath (relative to the ignore root, forward slashes)
// is ignored. A trailing slash marks relPath as a directory so directory-only
// patterns apply.
func (m Matcher) Ignores(relPath string) bool {
	relPath = strings.TrimLeft(strings.ReplaceAll(relPath, "\\", "/"), "/")
	isDir := strings.HasSuffix(relPath, "/")
	relPath = strings.TrimSuffix(relPath, "/")
	ignored := false
	for _, p := range m.patterns {
		if p.matches(relPath, isDir) {
			ignored = !p.negated
		}
	}
	return ignored
}

func (p pattern) matches(relPath string, isDir bool) bool {
	pat := strings.TrimLeft(strings.ReplaceAll(p.pattern, "\\", "/"), "/")
	if pat == "" {
		return false
	}
	if p.dirOnly && !isDir && relPath != pat && !strings.HasPrefix(relPath, pat+"/") {
		return false
	}
	if strings.Contains(pat, "/") {
		if wildcardMatch(pat, relPath) {
			return true
		}
		if p.dirOnly && (relPath == pat || strings.HasPrefix(relPath, pat+"/")) {
			return true
		}
		return false
	}
	for _, segment := range strings.Split(relPath, "/") {
		if wildcardMatch(pat, segment) {
			return true
		}
	}
	return false
}

func wildcardMatch(pat string, name string) bool {
	if matched, err := path.Match(pat, name); err == nil && matched {
		return true
	}
	return pat == name
}

// PrefixPattern normalizes a raw ignore-file line and prefixes it with the given
// directory (relative to the ignore root, e.g. "src/"). It returns "" for blank
// lines and comments. Negation and leading-slash anchoring are preserved.
func PrefixPattern(line string, prefix string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, `\#`) {
		return ""
	}
	pat := line
	negated := false
	if strings.HasPrefix(pat, "!") {
		negated = true
		pat = strings.TrimPrefix(pat, "!")
	} else if strings.HasPrefix(pat, `\!`) {
		pat = strings.TrimPrefix(pat, `\`)
	}
	pat = strings.TrimPrefix(pat, "/")
	prefixed := pat
	if prefix != "" {
		prefixed = prefix + pat
	}
	if negated {
		return "!" + prefixed
	}
	return prefixed
}
