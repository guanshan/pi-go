package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cautils "github.com/guanshan/pi-go/packages/coding-agent/utils"
)

func TestComparePackageVersions(t *testing.T) {
	if comparison, ok := ComparePackageVersions("v1.2.3", "1.2.2"); !ok || comparison <= 0 {
		t.Fatalf("comparison=%d ok=%v", comparison, ok)
	}
	if comparison, ok := ComparePackageVersions("1.2.3-beta", "1.2.3"); !ok || comparison >= 0 {
		t.Fatalf("prerelease comparison=%d ok=%v", comparison, ok)
	}
	if !IsNewerPackageVersion("not-semver-a", "not-semver-b") {
		t.Fatal("expected fallback string comparison to report change")
	}
}

func TestGetLatestPiRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing user agent")
		}
		_, _ = w.Write([]byte(`{"version":" 9.8.7 ","packageName":"pi","note":"hi"}`))
	}))
	defer server.Close()
	release, err := GetLatestPiRelease(context.Background(), "1.0.0", VersionCheckOptions{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if release == nil || release.Version != "9.8.7" || release.PackageName != "pi" || release.Note != "hi" {
		t.Fatalf("release=%#v", release)
	}
}

func TestParseChangelog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Changelog\n\n## [1.2.0]\n- new\n\n## 1.1.0\n- old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := cautils.ParseChangelog(path)
	if len(entries) != 2 {
		t.Fatalf("entries=%#v", entries)
	}
	newEntries := cautils.GetNewChangelogEntries(entries, "1.1.5")
	if len(newEntries) != 1 || newEntries[0].Minor != 2 {
		t.Fatalf("new=%#v", newEntries)
	}
}

func TestMigrateAuthToAuthJSON(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "oauth.json"), []byte(`{"anthropic":{"access":"a","refresh":"r","expires":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"apiKeys":{"openai":"key"},"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	providers, err := MigrateAuthToAuthJSON(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers=%#v", providers)
	}
	raw, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]map[string]any
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatal(err)
	}
	if auth["anthropic"]["type"] != "oauth" || auth["openai"]["key"] != "key" {
		t.Fatalf("auth=%#v", auth)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "oauth.json.migrated")); err != nil {
		t.Fatal(err)
	}
	settings, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(settings) == "" || json.Valid(settings) == false {
		t.Fatalf("invalid settings: %s", settings)
	}
	if containsBytes(settings, []byte("apiKeys")) {
		t.Fatalf("apiKeys not removed: %s", settings)
	}
}

func TestRunMigrationsSessionAndExtensions(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	session := `{"type":"session","cwd":"/root/project"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "s.jsonl"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(agentDir, "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(agentDir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := RunMigrations(cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "sessions", "--root-project--", "s.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "prompts")); err != nil {
		t.Fatal(err)
	}
	if len(result.DeprecationWarnings) == 0 {
		t.Fatalf("warnings=%#v", result.DeprecationWarnings)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
