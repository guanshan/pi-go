package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentDirPrefersCanonicalEnv(t *testing.T) {
	// TS config.ts recognizes only PI_CODING_AGENT_DIR (EnvLegacyAgentDir); the
	// short PI_AGENT_DIR (EnvAgentDir) is a Go-only alias. When both are set the
	// canonical TS name must win, matching TS behavior.
	canonical := filepath.Join(t.TempDir(), "canonical-agent")
	alias := filepath.Join(t.TempDir(), "alias-agent")
	t.Setenv(EnvAgentDir, alias)
	t.Setenv(EnvLegacyAgentDir, canonical)

	if got := AgentDir(); got != canonical {
		t.Fatalf("agent dir = %q, want canonical %q", got, canonical)
	}
}

func TestSessionDirSupportsLegacyEnv(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "legacy-sessions")
	t.Setenv(EnvLegacySessionDir, legacy)

	settings := NewSettingsManager(cwd, agentDir)
	if got := settings.SessionDir(); got != legacy {
		t.Fatalf("session dir = %q", got)
	}
}

func TestExpandTildePathAndPackageDir(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	if got := ExpandTildePath("~/sessions"); got != filepath.Join(home, "sessions") {
		t.Fatalf("expanded path = %q", got)
	}
	if got := GetPackageDir(); got == "" {
		t.Fatal("expected package dir")
	}
}

func TestSettingsManagerMigratesLegacySettings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{
		"queueMode": "all",
		"websockets": false,
		"skills": {
			"enableSkillCommands": false,
			"customDirectories": ["~/skills"]
		},
		"retry": {
			"maxDelayMs": 1234,
			"provider": {}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := NewSettingsManager(cwd, agentDir)
	if settings.SteeringMode() != "all" {
		t.Fatalf("steering=%q", settings.SteeringMode())
	}
	if settings.Transport() != "sse" {
		t.Fatalf("transport=%q", settings.Transport())
	}
	if settings.EnableSkillCommands() {
		t.Fatal("enableSkillCommands was not migrated")
	}
	if len(settings.Global.Skills) != 1 || settings.Global.Skills[0] != "~/skills" {
		t.Fatalf("skills=%#v", settings.Global.Skills)
	}
	if settings.ProviderRetryMaxDelayMS() != 1234 {
		t.Fatalf("provider max delay=%d", settings.ProviderRetryMaxDelayMS())
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}
