package ai

import "testing"

// TestEnvApiKeyGitHubCopilotOnlyFromCopilotToken ports
// ../pi/packages/ai/test/env-api-keys.test.ts (~lines 29-45). GitHub Copilot
// credentials must resolve ONLY from COPILOT_GITHUB_TOKEN; generic GitHub tokens
// (GH_TOKEN / GITHUB_TOKEN) must never be treated as Copilot credentials.
//
// findEnvKeys -> ProviderEnvKeys, getEnvApiKey -> GetEnvAPIKey.
func TestEnvApiKeyGitHubCopilotOnlyFromCopilotToken(t *testing.T) {
	// Case 1: generic GitHub tokens present, no COPILOT_GITHUB_TOKEN.
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	// Ensure COPILOT_GITHUB_TOKEN is unset for this case even if the host env had
	// it; t.Setenv restores prior state at the end of the test.
	t.Setenv("COPILOT_GITHUB_TOKEN", "")

	keys := ProviderEnvKeys("github-copilot")
	if len(keys) != 1 || keys[0] != "COPILOT_GITHUB_TOKEN" {
		t.Fatalf("ProviderEnvKeys(github-copilot)=%v, want [COPILOT_GITHUB_TOKEN]", keys)
	}
	if got := GetEnvAPIKey("github-copilot"); got != "" {
		t.Fatalf("generic GitHub tokens must not resolve as Copilot credentials, got %q", got)
	}

	// Case 2: COPILOT_GITHUB_TOKEN present alongside the generic tokens.
	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	keys = ProviderEnvKeys("github-copilot")
	if len(keys) != 1 || keys[0] != "COPILOT_GITHUB_TOKEN" {
		t.Fatalf("ProviderEnvKeys(github-copilot)=%v, want [COPILOT_GITHUB_TOKEN]", keys)
	}
	if got := GetEnvAPIKey("github-copilot"); got != "copilot-token" {
		t.Fatalf("GetEnvAPIKey(github-copilot)=%q, want copilot-token", got)
	}
}
