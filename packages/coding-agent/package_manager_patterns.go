package codingagent

import (
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
)

func applyPatterns(allPaths []string, patterns []string, baseDir string) map[string]bool {
	var includes, excludes, forceIncludes, forceExcludes []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		switch {
		case strings.HasPrefix(pattern, "+"):
			forceIncludes = append(forceIncludes, pattern[1:])
		case strings.HasPrefix(pattern, "-"):
			forceExcludes = append(forceExcludes, pattern[1:])
		case strings.HasPrefix(pattern, "!"):
			excludes = append(excludes, pattern[1:])
		default:
			includes = append(includes, pattern)
		}
	}

	enabled := map[string]bool{}
	for _, file := range allPaths {
		if len(includes) == 0 || matchesAnyPattern(file, includes, baseDir) {
			enabled[file] = true
		}
	}
	for _, file := range allPaths {
		if enabled[file] && matchesAnyPattern(file, excludes, baseDir) {
			enabled[file] = false
		}
	}
	for _, file := range allPaths {
		if !enabled[file] && matchesAnyExactPattern(file, forceIncludes, baseDir) {
			enabled[file] = true
		}
	}
	for _, file := range allPaths {
		if enabled[file] && matchesAnyExactPattern(file, forceExcludes, baseDir) {
			enabled[file] = false
		}
	}
	return enabled
}

func matchesAnyPattern(filePath string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel, _ := filepath.Rel(baseDir, filePath)
	candidates := patternCandidates(filePath, rel)
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		for _, candidate := range candidates {
			if globPatternMatch(pattern, candidate) {
				return true
			}
		}
	}
	return false
}

func matchesAnyExactPattern(filePath string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel, _ := filepath.Rel(baseDir, filePath)
	candidates := patternCandidates(filePath, rel)
	for _, pattern := range patterns {
		pattern = normalizeExactPattern(pattern)
		for _, candidate := range candidates {
			if candidate == pattern {
				return true
			}
		}
	}
	return false
}

func patternCandidates(filePath, rel string) []string {
	filePath = filepath.ToSlash(filepath.Clean(filePath))
	rel = filepath.ToSlash(filepath.Clean(rel))
	name := filepath.Base(filePath)
	candidates := []string{filePath, rel, name}
	if name == "SKILL.md" {
		parent := filepath.Dir(filePath)
		parentRel := filepath.ToSlash(filepath.Clean(filepath.Dir(rel)))
		candidates = append(candidates, filepath.ToSlash(parent), parentRel, filepath.Base(parent))
	}
	return uniqueStrings(candidates)
}

func normalizeExactPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, `.\\`)
	return filepath.ToSlash(filepath.Clean(pattern))
}

func globPatternMatch(pattern, candidate string) bool {
	pattern = filepath.ToSlash(pattern)
	candidate = filepath.ToSlash(candidate)
	if matched, err := pathpkg.Match(pattern, candidate); err == nil && matched {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return candidate == pattern || strings.HasSuffix(candidate, "/"+pattern)
	}
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*\*`, `.*`)
	re = strings.ReplaceAll(re, `\*`, `[^/]*`)
	re = strings.ReplaceAll(re, `\?`, `.`)
	matched, err := regexp.MatchString("^"+re+"$", candidate)
	return err == nil && matched
}

func resourceBaseDir(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Clean(path)
	}
	return filepath.Dir(path)
}

func firstMissingHandler(handlers []MissingSourceHandler) MissingSourceHandler {
	if len(handlers) == 0 {
		return nil
	}
	return handlers[0]
}
