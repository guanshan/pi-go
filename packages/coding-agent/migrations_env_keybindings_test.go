package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

// TestSkipsMigrations verifies the migration gate: default interactive (no
// args) and --help both run migrations (TS main.ts runs runMigrations before
// printHelp), while only paths that exit before runMigrations — export and pure
// package/config commands — skip.
func TestSkipsMigrations(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want bool
	}{
		{"default interactive no args", nil, false},
		{"empty slice", []string{}, false},
		{"interactive print", []string{"--print", "hi"}, false},
		{"interactive model", []string{"--model", "faux/faux"}, false},
		{"help long runs migrations", []string{"--help"}, false},
		{"help short runs migrations", []string{"-h"}, false},
		{"help with subcommand skips via package command", []string{"install", "--help"}, true},
		{"export with value", []string{"--export", "session.jsonl"}, true},
		{"export without value runs migrations", []string{"--export"}, false},
		{"install", []string{"install", "pkg"}, true},
		{"remove", []string{"remove", "pkg"}, true},
		{"uninstall", []string{"uninstall", "pkg"}, true},
		{"update", []string{"update"}, true},
		{"list", []string{"list"}, true},
		{"config", []string{"config", "--list"}, true},
		{"non-command first arg", []string{"hello"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skipsMigrations(tt.argv); got != tt.want {
				t.Fatalf("skipsMigrations(%v) = %v, want %v", tt.argv, got, tt.want)
			}
		})
	}
}

// TestMigrateModelsJSONConfigValues confirms a bare env-var name in a models.json
// provider apiKey is rewritten to explicit $ENV_VAR syntax, tolerating JSONC
// comments and trailing commas. Mirrors migrateModelsJsonConfigValues.
func TestMigrateModelsJSONConfigValues(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	jsonc := `{
  // custom models config
  "providers": {
    "openai": {
      "apiKey": "OPENAI_API_KEY",
      "headers": { "X-Token": "MY_TOKEN", "X-Literal": "literal-value" },
    },
    "anthropic": {
      "apiKey": "$ALREADY_EXPLICIT"
    },
  },
}`
	if err := os.WriteFile(modelsPath, []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}

	MigrateExplicitEnvVarConfigValues(agentDir)

	raw, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Providers map[string]struct {
			APIKey  string            `json:"apiKey"`
			Headers map[string]string `json:"headers"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("rewritten models.json not valid JSON: %v\n%s", err, raw)
	}
	if got := parsed.Providers["openai"].APIKey; got != "$OPENAI_API_KEY" {
		t.Fatalf("openai apiKey = %q, want $OPENAI_API_KEY", got)
	}
	if got := parsed.Providers["openai"].Headers["X-Token"]; got != "$MY_TOKEN" {
		t.Fatalf("openai header X-Token = %q, want $MY_TOKEN", got)
	}
	if got := parsed.Providers["openai"].Headers["X-Literal"]; got != "literal-value" {
		t.Fatalf("non-env literal header rewritten: %q", got)
	}
	if got := parsed.Providers["anthropic"].APIKey; got != "$ALREADY_EXPLICIT" {
		t.Fatalf("already-explicit apiKey changed: %q", got)
	}
}

// TestMigrateAuthJSONConfigValues confirms a bare env-var name in an auth.json
// api_key credential is rewritten and the file keeps owner-only permissions.
func TestMigrateAuthJSONConfigValues(t *testing.T) {
	agentDir := t.TempDir()
	authPath := filepath.Join(agentDir, "auth.json")
	auth := `{
  "openai": { "type": "api_key", "key": "OPENAI_API_KEY" },
  "anthropic": { "type": "oauth", "access": "tok" }
}`
	if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}

	MigrateExplicitEnvVarConfigValues(agentDir)

	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if got := parsed["openai"]["key"]; got != "$OPENAI_API_KEY" {
		t.Fatalf("openai key = %q, want $OPENAI_API_KEY", got)
	}
	if _, ok := parsed["anthropic"]["key"]; ok {
		t.Fatalf("oauth credential unexpectedly gained a key: %v", parsed["anthropic"])
	}
}

// TestMigrateKeybindingsConfigFile verifies legacy keybinding names are renamed
// to their current names and the file is re-written in canonical order. Mirrors
// migrateKeybindingsConfigFile + migrateKeybindingsConfig.
func TestMigrateKeybindingsConfigFile(t *testing.T) {
	agentDir := t.TempDir()
	configPath := filepath.Join(agentDir, "keybindings.json")
	old := `{
  "cursorUp": "up",
  "newSession": "ctrl+n",
  "interrupt": ["escape", "ctrl+c"],
  "customExtra": "ctrl+q"
}`
	if err := os.WriteFile(configPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	MigrateKeybindingsConfigFile(agentDir)

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("rewritten keybindings.json not valid JSON: %v\n%s", err, raw)
	}
	for _, legacy := range []string{"cursorUp", "newSession", "interrupt"} {
		if _, ok := parsed[legacy]; ok {
			t.Fatalf("legacy key %q still present", legacy)
		}
	}
	for _, current := range []string{"tui.editor.cursorUp", "app.session.new", "app.interrupt", "customExtra"} {
		if _, ok := parsed[current]; !ok {
			t.Fatalf("expected key %q missing after migration", current)
		}
	}
	var up string
	if err := json.Unmarshal(parsed["tui.editor.cursorUp"], &up); err != nil || up != "up" {
		t.Fatalf("tui.editor.cursorUp = %q (err %v), want up", up, err)
	}
}

// TestMigrateKeybindingsDropsLegacyWhenTargetPresent verifies that when both a
// legacy name and its renamed target exist, the renamed target's value wins and
// the legacy entry is dropped. Mirrors the Object.hasOwn branch in TS.
func TestMigrateKeybindingsDropsLegacyWhenTargetPresent(t *testing.T) {
	agentDir := t.TempDir()
	configPath := filepath.Join(agentDir, "keybindings.json")
	old := `{
  "newSession": "ctrl+n",
  "app.session.new": "ctrl+m"
}`
	if err := os.WriteFile(configPath, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	MigrateKeybindingsConfigFile(agentDir)

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["newSession"]; ok {
		t.Fatal("legacy newSession should be dropped when app.session.new exists")
	}
	if parsed["app.session.new"] != "ctrl+m" {
		t.Fatalf("app.session.new = %q, want preserved ctrl+m", parsed["app.session.new"])
	}
}

// TestRunMigrationsRunsEnvVarAndKeybindings confirms RunMigrations wires in the
// env-var and keybindings migrations end-to-end.
func TestRunMigrationsRunsEnvVarAndKeybindings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"),
		[]byte(`{"providers":{"openai":{"apiKey":"OPENAI_API_KEY"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"),
		[]byte(`{"newSession":"ctrl+n"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := RunMigrations(cwd, agentDir); err != nil {
		t.Fatal(err)
	}

	models, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(models, []byte("$OPENAI_API_KEY")) {
		t.Fatalf("env-var migration did not run: %s", models)
	}
	bindings, err := os.ReadFile(filepath.Join(agentDir, "keybindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(bindings, []byte("app.session.new")) {
		t.Fatalf("keybindings migration did not run: %s", bindings)
	}
}

// TestStripJSONComments covers comment and trailing-comma stripping while
// leaving string contents (including // inside strings) untouched.
func TestStripJSONComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"line comment", "{\n  \"a\": 1 // trailing\n}", "{\n  \"a\": 1 \n}"},
		{"trailing comma object", `{"a":1,}`, `{"a":1}`},
		{"trailing comma array", `[1,2,]`, `[1,2]`},
		{"slashes in string kept", `{"u":"http://x//y"}`, `{"u":"http://x//y"}`},
		{"comma in string kept", `{"a":"x,","b":2}`, `{"a":"x,","b":2}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(stripJSONComments([]byte(tt.in)))
			if got != tt.want {
				t.Fatalf("stripJSONComments(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("result not valid JSON: %q", got)
			}
		})
	}
}

// TestIsLegacyEnvVarNameConfigValue covers the legacy bare-name detection.
func TestIsLegacyEnvVarNameConfigValue(t *testing.T) {
	cases := map[string]bool{
		"OPENAI_API_KEY":  true,
		"_PRIVATE":        true,
		"A1_B2":           true,
		"$OPENAI_API_KEY": false,
		"lowercase":       false,
		"Mixed_CASE":      false,
		"sk-abc123":       false,
		"":                false,
	}
	for value, want := range cases {
		if got := isLegacyEnvVarNameConfigValue(value); got != want {
			t.Fatalf("isLegacyEnvVarNameConfigValue(%q) = %v, want %v", value, got, want)
		}
	}
}

// TestMigratedCredentialDirIsNotWorldTraversable confirms that creating a fresh
// auth.json during migration uses a 0700 parent directory so the credential
// boundary is not world-traversable. Mirrors auth-storage.ts dir permissions.
func TestMigratedCredentialDirIsNotWorldTraversable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not meaningful on Windows")
	}
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	// settings.json with apiKeys triggers a fresh auth.json under agentDir.
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"),
		[]byte(`{"apiKeys":{"openai":"sk-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateAuthToAuthJSON(agentDir); err != nil {
		t.Fatal(err)
	}

	// The auth.json itself must be 0600.
	authInfo, err := os.Stat(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := authInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json perm = %o, want 600", perm)
	}

	// A credential directory created by writeIndentedJSON for a 0600 file must
	// be 0700 (no group/other access bits).
	nested := filepath.Join(root, "creds", "auth.json")
	if err := writeIndentedJSON(nested, map[string]string{"k": "v"}, 0o600); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("credential dir perm = %o, want no group/other bits", perm)
	}

	wideDir := filepath.Join(root, "existing-creds")
	if err := os.MkdirAll(wideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingNested := filepath.Join(wideDir, "auth.json")
	if err := writeIndentedJSON(existingNested, map[string]string{"k": "v"}, 0o600); err != nil {
		t.Fatal(err)
	}
	existingDirInfo, err := os.Stat(wideDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := existingDirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("existing credential dir perm = %o, want 700", perm)
	}
}

// TestMainNoArgGate is a lightweight integration-style guard that a default
// (interactive) invocation does not short-circuit the migration step. It
// exercises the gate decision rather than launching the TUI.
func TestMainNoArgGate(t *testing.T) {
	if skipsMigrations(nil) {
		t.Fatal("no-arg default interactive must run migrations")
	}
	// Sanity: the env-var-driven agent dir is honored by the migration helpers.
	agentDir := t.TempDir()
	t.Setenv(core.EnvAgentDir, agentDir)
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"),
		[]byte(`{"interrupt":"escape"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	MigrateKeybindingsConfigFile()
	raw, err := os.ReadFile(filepath.Join(agentDir, "keybindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(raw, []byte("app.interrupt")) {
		t.Fatalf("keybindings migration using env agent dir did not run: %s", raw)
	}
}
