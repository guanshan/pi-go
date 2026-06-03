package ai

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAuthStorageAPIKeyPrecedenceAndPersistence(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	auth.SetRuntime("openai", "runtime-key")
	if got := auth.APIKey(Model{Provider: "openai", EnvKey: "OPENAI_API_KEY"}); got != "runtime-key" {
		t.Fatalf("runtime key=%q", got)
	}

	if err := auth.SaveAPIKey("anthropic", "stored-key"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewAuthStorage(dir)
	if got := reloaded.APIKey(Model{Provider: "anthropic"}); got != "stored-key" {
		t.Fatalf("stored key=%q", got)
	}
}

func TestAuthStorageCreatesPrivateParentDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	// A fresh agent dir that does not exist yet must be created 0700 with the
	// auth file 0600 (matching src/core/auth-storage.ts).
	agentDir := filepath.Join(t.TempDir(), "nested", "agent")
	auth := NewAuthStorage(agentDir)
	if err := auth.SaveAPIKey("anthropic", "stored-key"); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("auth dir perm=%o, want 0700", perm)
	}
	fileInfo, err := os.Stat(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth file perm=%o, want 0600", perm)
	}
}

func TestAuthStorageTightensExistingParentDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStorage(agentDir)
	if err := auth.SaveAPIKey("anthropic", "stored-key"); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("auth dir perm=%o, want 0700", perm)
	}
}

// P2-6: auth.json must be written with 2-space indent, matching TS
// (auth-storage.ts:219,290,447), not tabs.
func TestAuthStorageWritesTwoSpaceIndent(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	if err := auth.SaveAPIKey("anthropic", "stored-key"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"anthropic\"") {
		t.Fatalf("expected 2-space indent, got:\n%s", data)
	}
	if strings.Contains(string(data), "\n\t") {
		t.Fatalf("auth.json must not use tab indent, got:\n%s", data)
	}
}

func TestAuthStorageReadsObjectCredentialsAndEnvFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/auth.json", []byte(`{"openai":{"type":"oauth","access":"oauth-key"},"ANTHROPIC_API_KEY":"anthropic-api","ANTHROPIC_OAUTH_TOKEN":"anthropic-oauth","MOONSHOT_API_KEY":"moonshot-key","CUSTOM_MODEL_KEY":"model-env-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStorage(dir)
	if got := auth.APIKey(Model{Provider: "openai"}); got != "oauth-key" {
		t.Fatalf("oauth key=%q", got)
	}
	if got := auth.APIKey(Model{Provider: "anthropic"}); got != "anthropic-oauth" {
		t.Fatalf("anthropic stored env key precedence=%q", got)
	}
	if got := auth.APIKey(Model{Provider: "moonshotai-cn"}); got != "moonshot-key" {
		t.Fatalf("moonshot stored env key=%q", got)
	}
	if got := auth.APIKey(Model{Provider: "custom-model", EnvKey: "CUSTOM_MODEL_KEY"}); got != "model-env-key" {
		t.Fatalf("model env key=%q", got)
	}

	t.Setenv("CUSTOM_API_KEY", "env-key")
	if got := auth.APIKey(Model{Provider: "custom"}); got != "env-key" {
		t.Fatalf("env key=%q", got)
	}
}

func TestAuthStorageListStatusHasAuthAndDelete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/auth.json", []byte(`{"OPENAI_API_KEY":"legacy-key","anthropic":{"type":"oauth","access":"oauth-key"},"openai":{"type":"api_key","key":"stored-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthStorage(dir)
	if got := auth.List(); len(got) != 2 || got[0] != "anthropic" || got[1] != "openai" {
		t.Fatalf("list=%#v", got)
	}
	if !auth.Has("openai") || !auth.HasAuth("openai") {
		t.Fatal("expected openai stored auth")
	}
	status := auth.AuthStatus("anthropic")
	if !status.Configured || status.Source != "stored" || status.Type != "oauth" {
		t.Fatalf("anthropic status=%#v", status)
	}
	if got := auth.AuthStatus("custom"); got.Configured || got.Source != "" {
		t.Fatalf("custom status=%#v", got)
	}
	auth.SetRuntime("custom", "runtime-key")
	if got := auth.AuthStatus("custom"); got.Configured || got.Source != "runtime" || got.Label != "--api-key" {
		t.Fatalf("runtime status=%#v", got)
	}
	if err := auth.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewAuthStorage(dir)
	if reloaded.Has("openai") {
		t.Fatal("openai credential was not deleted")
	}
}

// TestAuthStatusBedrockAmbientCredentials covers P2-02c: ambient AWS auth
// (AWS_PROFILE / IAM keys / container / web-identity) must report bedrock as
// configured via an "environment" source, matching TS getEnvApiKey/getAuthStatus
// which resolves these sources to "<authenticated>".
func TestAuthStatusBedrockAmbientCredentials(t *testing.T) {
	// Clear every ambient AWS source so the test starts from a clean slate.
	for _, env := range []string{
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_PROFILE", "AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
	} {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}

	auth := NewAuthStorage(t.TempDir())
	if got := auth.AuthStatus("amazon-bedrock"); got.Configured || got.Source != "" {
		t.Fatalf("expected unconfigured bedrock with no ambient creds, got %#v", got)
	}

	// AWS_PROFILE alone is enough ambient auth for bedrock.
	t.Setenv("AWS_PROFILE", "my-profile")
	got := auth.AuthStatus("amazon-bedrock")
	if !got.Configured || got.Source != "environment" || got.Label == "" {
		t.Fatalf("expected bedrock configured via ambient AWS_PROFILE, got %#v", got)
	}

	// IAM access+secret keys also count as ambient auth.
	os.Unsetenv("AWS_PROFILE")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	if got := auth.AuthStatus("amazon-bedrock"); !got.Configured || got.Source != "environment" {
		t.Fatalf("expected bedrock configured via ambient IAM keys, got %#v", got)
	}
}

func TestProviderEnvKeysDefaultFallback(t *testing.T) {
	if got := ProviderEnvKeys("anthropic"); len(got) != 2 || got[0] != "ANTHROPIC_OAUTH_TOKEN" || got[1] != "ANTHROPIC_API_KEY" {
		t.Fatalf("anthropic env keys=%#v", got)
	}
	if got := ProviderEnvKeys("github-copilot"); len(got) != 1 || got[0] != "COPILOT_GITHUB_TOKEN" {
		t.Fatalf("copilot env keys=%#v", got)
	}
	for provider, want := range map[string]string{
		"minimax":               "MINIMAX_API_KEY",
		"minimax-cn":            "MINIMAX_CN_API_KEY",
		"moonshotai-cn":         "MOONSHOT_API_KEY",
		"huggingface":           "HF_TOKEN",
		"opencode":              "OPENCODE_API_KEY",
		"opencode-go":           "OPENCODE_API_KEY",
		"xiaomi":                "XIAOMI_API_KEY",
		"xiaomi-token-plan-cn":  "XIAOMI_TOKEN_PLAN_CN_API_KEY",
		"xiaomi-token-plan-ams": "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
		"xiaomi-token-plan-sgp": "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
	} {
		if got := ProviderEnvKeys(provider); len(got) != 1 || got[0] != want {
			t.Fatalf("%s env keys=%#v", provider, got)
		}
	}
	if got := ProviderEnvKeys("my-provider"); len(got) != 1 || got[0] != "MY_PROVIDER_API_KEY" {
		t.Fatalf("env keys=%#v", got)
	}
	if got := ProviderEnvKeys(""); len(got) != 0 {
		t.Fatalf("empty provider env keys=%#v", got)
	}
}
