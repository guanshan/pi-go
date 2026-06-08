package core

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestSlashLoginOAuthSelectUsesSelectPrompter(t *testing.T) {
	var selectedByProvider string
	ai.RegisterOAuthProvider(ai.OAuthProvider{
		ProviderID:   "test-oauth-select",
		ProviderName: "Test OAuth Select",
		LoginFunc: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
			id, ok, err := callbacks.OnSelect(ai.OAuthSelectPrompt{
				Message: "Select login method",
				Options: []ai.OAuthSelectOption{
					{ID: "max", Label: "Claude Pro/Max"},
					{ID: "console", Label: "API Console"},
				},
			})
			if err != nil {
				return ai.OAuthCredentials{}, err
			}
			if !ok {
				return ai.OAuthCredentials{}, fmt.Errorf("cancelled")
			}
			selectedByProvider = id
			return ai.OAuthCredentials{Access: "oauth-" + id, Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
		},
	})
	defer ai.UnregisterOAuthProvider("test-oauth-select")

	agent := newAuthSlashTestAgent(t)
	var stdout, stderr bytes.Buffer
	textCalled := false
	textPrompter := func(ai.OAuthPrompt) (string, error) {
		textCalled = true
		return "", nil
	}
	selectPrompter := func(prompt ai.OAuthSelectPrompt) (string, bool, error) {
		if len(prompt.Options) != 2 {
			t.Fatalf("select prompt options=%d, want 2", len(prompt.Options))
		}
		return "console", true, nil
	}
	if _, err := handleSlashWithPrompt(context.Background(), agent, "/login test-oauth-select --oauth", textPrompter, selectPrompter, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if selectedByProvider != "console" {
		t.Fatalf("provider received id=%q, want console", selectedByProvider)
	}
	if textCalled {
		t.Fatal("text prompter must not be used when a select prompter is provided")
	}
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "test-oauth-select"}); got != "oauth-console" {
		t.Fatalf("saved access=%q, want oauth-console", got)
	}
}

func TestSlashLoginListsProvidersAndSavesAPIKey(t *testing.T) {
	agent := newAuthSlashTestAgent(t)
	var stdout, stderr bytes.Buffer

	done, err := handleSlash(context.Background(), agent, "/login", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("login should not exit")
	}
	output := stdout.String()
	if !strings.Contains(output, "Provider authentication:") || !strings.Contains(output, "openai") || !strings.Contains(output, "Usage: /login <provider> <api-key>") {
		t.Fatalf("login overview output=%q", output)
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), agent, "/login openai --api-key sk-test", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "openai"}); got != "sk-test" {
		t.Fatalf("saved key=%q", got)
	}
	if !strings.Contains(stdout.String(), "Saved API key for openai") {
		t.Fatalf("save output=%q", stdout.String())
	}
}

func TestSlashLogoutListsAndRemovesStoredCredentials(t *testing.T) {
	agent := newAuthSlashTestAgent(t)
	if err := agent.Registry.Auth.SaveAPIKey("test-provider", "sk-test"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	if _, err := handleSlash(context.Background(), agent, "/logout", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "test-provider"}); got != "sk-test" {
		t.Fatalf("logout without provider deleted key=%q", got)
	}
	if output := stdout.String(); !strings.Contains(output, "Stored credentials:") || !strings.Contains(output, "Usage: /logout <provider>") {
		t.Fatalf("logout list output=%q", output)
	}

	stdout.Reset()
	if _, err := handleSlash(context.Background(), agent, "/logout test-provider", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "test-provider"}); got != "" {
		t.Fatalf("key was not removed: %q", got)
	}
	if !strings.Contains(stdout.String(), "Removed stored credentials for test-provider") {
		t.Fatalf("remove output=%q", stdout.String())
	}
}

func TestSlashLoginOAuthProviderSavesCredentials(t *testing.T) {
	ai.RegisterOAuthProvider(ai.OAuthProvider{
		ProviderID:   "test-oauth",
		ProviderName: "Test OAuth",
		LoginFunc: func(callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
			if callbacks.OnAuth != nil {
				callbacks.OnAuth(ai.OAuthAuthInfo{URL: "https://example.test/auth"})
			}
			if callbacks.OnProgress != nil {
				callbacks.OnProgress("progress")
			}
			return ai.OAuthCredentials{
				Access:  "oauth-access",
				Refresh: "oauth-refresh",
				Expires: time.Now().Add(time.Hour).UnixMilli(),
			}, nil
		},
	})
	defer ai.UnregisterOAuthProvider("test-oauth")

	agent := newAuthSlashTestAgent(t)
	var stdout, stderr bytes.Buffer
	if _, err := handleSlashWithPrompt(context.Background(), agent, "/login test-oauth --oauth", func(ai.OAuthPrompt) (string, error) {
		return "", nil
	}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := agent.Registry.Auth.APIKey(ai.Model{Provider: "test-oauth"}); got != "oauth-access" {
		t.Fatalf("oauth access=%q", got)
	}
	status := agent.Registry.Auth.AuthStatus("test-oauth")
	if !status.Configured || status.Type != "oauth" {
		t.Fatalf("status=%#v", status)
	}
	output := stdout.String()
	if !strings.Contains(output, "https://example.test/auth") || !strings.Contains(output, "Saved OAuth credentials for test-oauth") {
		t.Fatalf("output=%q", output)
	}
}

func newAuthSlashTestAgent(t *testing.T) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	auth := ai.NewAuthStorage(settings.AgentDir)
	registry := ai.NewModelRegistry(settings.AgentDir, auth)
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	return NewAgentSession(InMemorySession(cwd), settings, registry, resources, model, ai.ThinkingOff, ToolSet{}, "system")
}
