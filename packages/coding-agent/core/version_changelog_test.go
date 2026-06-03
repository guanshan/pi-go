package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestVersionDerivedFromAI locks core.Version to the single source of truth in
// packages/ai (P1-09), so the two cannot drift.
func TestVersionDerivedFromAI(t *testing.T) {
	if Version != ai.Version {
		t.Fatalf("core.Version=%q, want ai.Version=%q", Version, ai.Version)
	}
}

func writeChangelog(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestChangelogForDisplayFreshInstall records the version and shows nothing on a
// fresh install (no recorded lastChangelogVersion).
func TestChangelogForDisplayFreshInstall(t *testing.T) {
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	path := writeChangelog(t, dir, "## [0.78.0]\n- new thing\n")

	got := ChangelogForDisplay(settings, false, path)
	if got != "" {
		t.Fatalf("fresh install must not show changelog, got %q", got)
	}
	if settings.LastChangelogVersion() != ai.UpstreamVersion {
		t.Fatalf("fresh install must record version, got %q", settings.LastChangelogVersion())
	}
}

// TestChangelogForDisplayAfterUpgrade shows the entries newer than the recorded
// version exactly once and records the new version.
func TestChangelogForDisplayAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	settings.Global.LastChangelogVersion = "0.77.0"
	path := writeChangelog(t, dir, "## [0.78.0]\n- shiny feature\n\n## [0.77.0]\n- old\n")

	got := ChangelogForDisplay(settings, false, path)
	if !strings.Contains(got, "shiny feature") {
		t.Fatalf("upgrade must show new entries, got %q", got)
	}
	if strings.Contains(got, "old") {
		t.Fatalf("upgrade must not show already-seen entries, got %q", got)
	}
	if settings.LastChangelogVersion() != ai.UpstreamVersion {
		t.Fatalf("upgrade must record current version, got %q", settings.LastChangelogVersion())
	}

	// Second call (now up to date) shows nothing.
	if again := ChangelogForDisplay(settings, false, path); again != "" {
		t.Fatalf("changelog must show only once, second call returned %q", again)
	}
}

// TestChangelogForDisplaySkipsResumedSession never shows the changelog for a
// session that already has messages (resumed/continued).
func TestChangelogForDisplaySkipsResumedSession(t *testing.T) {
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	settings.Global.LastChangelogVersion = "0.77.0"
	path := writeChangelog(t, dir, "## [0.78.0]\n- shiny feature\n")

	got := ChangelogForDisplay(settings, true, path)
	if got != "" {
		t.Fatalf("resumed session must not show changelog, got %q", got)
	}
	// Recorded version is unchanged so it will still show on a fresh session.
	if settings.LastChangelogVersion() != "0.77.0" {
		t.Fatalf("resumed session must not record version, got %q", settings.LastChangelogVersion())
	}
}

// TestLastChangelogVersionPersists verifies the setting round-trips through
// global settings persistence.
func TestLastChangelogVersionPersists(t *testing.T) {
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	settings.SetLastChangelogVersion("0.78.0")
	if err := settings.SaveGlobal(); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSettingsManager(dir, dir)
	if reloaded.LastChangelogVersion() != "0.78.0" {
		t.Fatalf("lastChangelogVersion did not persist, got %q", reloaded.LastChangelogVersion())
	}
}
