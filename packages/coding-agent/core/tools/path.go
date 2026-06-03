package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func ResolveInCWD(cwd, path string) string {
	path = normalizePathInput(path, false)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

// ResolveToolPath resolves a file/dir path for the write/edit/grep/find/ls
// tools. It strips a bare leading "@" (CLI @file syntax) before resolving,
// matching the TS resolveToCwd which uses stripAtPrefix:true for all of these
// tools. A literal "./@file" is unaffected because only a leading "@" is
// stripped.
func ResolveToolPath(cwd, path string) string {
	return ResolveInCWD(cwd, normalizePathInput(path, true))
}

var macOSScreenshotAMPMPattern = regexp.MustCompile(` (?i:am|pm)\.`)

func ResolveReadPath(cwd, path string) string {
	resolved := ResolveInCWD(cwd, normalizePathInput(path, true))
	if fileExists(resolved) {
		return resolved
	}
	for _, candidate := range readPathVariants(resolved) {
		if candidate != resolved && fileExists(candidate) {
			return candidate
		}
	}
	return resolved
}

func readPathVariants(path string) []string {
	amPM := macOSScreenshotAMPMPattern.ReplaceAllStringFunc(path, func(match string) string {
		return "\u202f" + match[1:]
	})
	nfd := norm.NFD.String(path)
	curly := strings.ReplaceAll(path, "'", "\u2019")
	return []string{
		amPM,
		nfd,
		curly,
		strings.ReplaceAll(nfd, "'", "\u2019"),
	}
}

func normalizePathInput(path string, stripAtPrefix bool) string {
	if stripAtPrefix && strings.HasPrefix(path, "@") {
		path = strings.TrimPrefix(path, "@")
	}
	path = strings.Map(func(r rune) rune {
		switch {
		case r == '\u00a0' || r == '\u202f' || r == '\u205f' || r == '\u3000':
			return ' '
		case r >= '\u2000' && r <= '\u200a':
			return ' '
		default:
			return r
		}
	}, path)
	// Only a genuine `file://` URL is decoded (TS paths.ts: /^file:\/\//). A bare
	// `file:foo` is treated as a plain relative path, not a URL.
	if strings.HasPrefix(path, "file://") {
		if decoded, ok := FileURLToPath(path); ok {
			path = decoded
		}
	}
	path = expandTilde(path)
	return strings.TrimFunc(path, unicode.IsControl)
}

func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
