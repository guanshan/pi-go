package cautils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndFormatChangelog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Changelog\n\n## [1.2.0]\n- new\n\n## 1.1.0\n- old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := ParseChangelog(path)
	if len(entries) != 2 {
		t.Fatalf("entries=%#v", entries)
	}
	newEntries := GetNewChangelogEntries(entries, "1.1.5")
	if len(newEntries) != 1 || newEntries[0].Minor != 2 {
		t.Fatalf("new=%#v", newEntries)
	}
	rendered := FormatChangelogMarkdown(entries)
	if !strings.Contains(rendered, "## 1.1.0") || !strings.Contains(rendered, "## [1.2.0]") {
		t.Fatalf("rendered changelog = %q", rendered)
	}
	if strings.Index(rendered, "## 1.1.0") > strings.Index(rendered, "## [1.2.0]") {
		t.Fatalf("rendered changelog should be oldest-to-newest after reverse: %q", rendered)
	}
}
