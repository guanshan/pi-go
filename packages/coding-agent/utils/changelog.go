package cautils

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ChangelogEntry struct {
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Patch   int    `json:"patch"`
	Content string `json:"content"`
}

func ChangelogPath() string {
	if explicit := os.Getenv("PI_CHANGELOG_PATH"); explicit != "" {
		return expandTilde(explicit)
	}
	if packageDir := os.Getenv("PI_PACKAGE_DIR"); packageDir != "" {
		return filepath.Join(expandTilde(packageDir), "CHANGELOG.md")
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		candidate := filepath.Join(filepath.Dir(exe), "CHANGELOG.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "CHANGELOG.md"
}

func ParseChangelog(changelogPath string) []ChangelogEntry {
	raw, err := os.ReadFile(changelogPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	headerPattern := regexp.MustCompile(`^##\s+\[?(\d+)\.(\d+)\.(\d+)\]?`)
	var entries []ChangelogEntry
	var current *ChangelogEntry
	var currentLines []string
	flush := func() {
		if current == nil || len(currentLines) == 0 {
			return
		}
		entry := *current
		entry.Content = strings.TrimSpace(strings.Join(currentLines, "\n"))
		entries = append(entries, entry)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			match := headerPattern.FindStringSubmatch(line)
			if len(match) != 4 {
				current = nil
				currentLines = nil
				continue
			}
			major, _ := strconv.Atoi(match[1])
			minor, _ := strconv.Atoi(match[2])
			patch, _ := strconv.Atoi(match[3])
			current = &ChangelogEntry{Major: major, Minor: minor, Patch: patch}
			currentLines = []string{line}
			continue
		}
		if current != nil {
			currentLines = append(currentLines, line)
		}
	}
	flush()
	return entries
}

func CompareChangelogVersions(v1, v2 ChangelogEntry) int {
	if v1.Major != v2.Major {
		return v1.Major - v2.Major
	}
	if v1.Minor != v2.Minor {
		return v1.Minor - v2.Minor
	}
	return v1.Patch - v2.Patch
}

func GetNewChangelogEntries(entries []ChangelogEntry, lastVersion string) []ChangelogEntry {
	parts := strings.Split(lastVersion, ".")
	last := ChangelogEntry{}
	if len(parts) > 0 {
		last.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		last.Minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		last.Patch, _ = strconv.Atoi(parts[2])
	}
	var out []ChangelogEntry
	for _, entry := range entries {
		if CompareChangelogVersions(entry, last) > 0 {
			out = append(out, entry)
		}
	}
	return out
}

func FormatChangelogMarkdown(entries []ChangelogEntry) string {
	if len(entries) == 0 {
		return "No changelog entries found."
	}
	var parts []string
	for i := len(entries) - 1; i >= 0; i-- {
		if strings.TrimSpace(entries[i].Content) != "" {
			parts = append(parts, entries[i].Content)
		}
	}
	if len(parts) == 0 {
		return "No changelog entries found."
	}
	return strings.Join(parts, "\n\n")
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
