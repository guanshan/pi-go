package ai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai/openaicodexauth"
)

func TestGeneratePKCE(t *testing.T) {
	pair, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.Verifier) < 43 || len(pair.Verifier) > 128 {
		t.Fatalf("unexpected verifier length: %d", len(pair.Verifier))
	}
	sum := sha256.Sum256([]byte(pair.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pair.Challenge != want {
		t.Fatalf("challenge mismatch: got %q want %q", pair.Challenge, want)
	}
}

func TestPollOAuthDeviceCodeFlowComplete(t *testing.T) {
	calls := 0
	token, err := PollOAuthDeviceCodeFlow(context.Background(), OAuthDeviceCodePollOptions{
		Sleep: func(context.Context, time.Duration) error { return nil },
		Poll: func(context.Context) (OAuthDeviceCodePollResult, error) {
			calls++
			if calls == 1 {
				return OAuthDeviceCodePollResult{Status: OAuthDeviceCodePending}, nil
			}
			return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeComplete, AccessToken: "token"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "token" || calls != 2 {
		t.Fatalf("token=%q calls=%d", token, calls)
	}
}

func TestPollOAuthDeviceCodeFlowPollsBeforeSleep(t *testing.T) {
	slept := false
	polledBeforeSleep := false
	token, err := PollOAuthDeviceCodeFlow(context.Background(), OAuthDeviceCodePollOptions{
		Sleep: func(context.Context, time.Duration) error {
			slept = true
			return nil
		},
		Poll: func(context.Context) (OAuthDeviceCodePollResult, error) {
			polledBeforeSleep = !slept
			return OAuthDeviceCodePollResult{Status: OAuthDeviceCodeComplete, AccessToken: "token"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "token" || !polledBeforeSleep {
		t.Fatalf("token=%q polledBeforeSleep=%v", token, polledBeforeSleep)
	}
}

func TestPollOAuthDeviceCodeFlowCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions{
		Sleep: func(context.Context, time.Duration) error { return nil },
		Poll:  func(context.Context) (OAuthDeviceCodePollResult, error) { return OAuthDeviceCodePollResult{}, nil },
	})
	if err == nil || err.Error() != cancelMessage {
		t.Fatalf("err=%v", err)
	}
}

func TestOAuthRegistryAndAPIKeyRefresh(t *testing.T) {
	ResetOAuthProviders()
	defer ResetOAuthProviders()
	RegisterOAuthProvider(OAuthProvider{
		ProviderID:   "test",
		ProviderName: "Test",
		RefreshTokenFunc: func(context.Context, OAuthCredentials) (OAuthCredentials, error) {
			return OAuthCredentials{Access: "new", Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
		},
	})
	result, err := GetOAuthAPIKey(context.Background(), "test", map[OAuthProviderID]OAuthCredentials{
		"test": {Access: "old", Refresh: "r", Expires: time.Now().Add(-time.Second).UnixMilli()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.APIKey != "new" {
		t.Fatalf("result=%#v", result)
	}
	UnregisterOAuthProvider("test")
	if _, ok := GetOAuthProvider("test"); ok {
		t.Fatal("custom provider was not removed")
	}
}

func TestModelRegistryAPIKeyRefreshesExpiredOAuthCredential(t *testing.T) {
	ResetOAuthProviders()
	defer ResetOAuthProviders()
	RegisterOAuthProvider(OAuthProvider{
		ProviderID:   "refresh-test",
		ProviderName: "Refresh Test",
		RefreshTokenFunc: func(context.Context, OAuthCredentials) (OAuthCredentials, error) {
			return OAuthCredentials{
				Access:  "new-access",
				Refresh: "new-refresh",
				Expires: time.Now().Add(time.Hour).UnixMilli(),
			}, nil
		},
	})
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	if err := auth.SaveOAuth("refresh-test", OAuthCredentials{
		Access:  "old-access",
		Refresh: "old-refresh",
		Expires: time.Now().Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir, auth)
	key, err := registry.APIKey(context.Background(), Model{Provider: "refresh-test"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "new-access" {
		t.Fatalf("key=%q", key)
	}
	reloaded := NewAuthStorage(dir)
	if got := reloaded.APIKey(Model{Provider: "refresh-test"}); got != "new-access" {
		t.Fatalf("reloaded key=%q", got)
	}
}

func TestOAuthCredentialsPreserveExtra(t *testing.T) {
	var credentials OAuthCredentials
	if err := json.Unmarshal([]byte(`{"access":"a","refresh":"r","expires":42,"enterpriseUrl":"ghe.example.com"}`), &credentials); err != nil {
		t.Fatal(err)
	}
	if credentials.Extra["enterpriseUrl"] != "ghe.example.com" {
		t.Fatalf("extra=%#v", credentials.Extra)
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !containsJSONField(raw, "enterpriseUrl") {
		t.Fatalf("raw=%s", raw)
	}
}

func TestOAuthModifyModelsAppliesGitHubCopilotEnterpriseBaseURL(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	if err := auth.SaveOAuth("github-copilot", OAuthCredentials{
		Access:  "copilot-token",
		Refresh: "github-token",
		Expires: time.Now().Add(time.Hour).UnixMilli(),
		Extra:   map[string]any{"enterpriseUrl": "https://ghe.example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir, auth)
	model, ok := registry.Find("github-copilot", "gpt-5-mini")
	if !ok {
		t.Fatal("missing github-copilot model")
	}
	if model.BaseURL != "https://copilot-api.ghe.example.com" {
		t.Fatalf("baseURL=%q", model.BaseURL)
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	tests := []struct {
		input string
		code  string
		state string
	}{
		{"https://localhost/callback?code=abc&state=xyz", "abc", "xyz"},
		{"abc#xyz", "abc", "xyz"},
		{"code=abc&state=xyz", "abc", "xyz"},
		{"abc", "abc", ""},
	}
	for _, tt := range tests {
		code, state, err := parseAuthorizationInput(tt.input)
		if err != nil {
			t.Fatalf("%q err=%v", tt.input, err)
		}
		if code != tt.code || state != tt.state {
			t.Fatalf("%q code=%q state=%q", tt.input, code, state)
		}
	}
}

func TestGitHubCopilotLoginDeviceFlow(t *testing.T) {
	var tokenPolls int
	var enabled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") != gitHubCopilotOAuthClientID {
				t.Fatalf("client_id=%q", r.Form.Get("client_id"))
			}
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":1,"expires_in":30}`))
		case "/login/oauth/access_token":
			tokenPolls++
			if tokenPolls == 1 {
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"github-access"}`))
		case "/copilot_internal/v2/token":
			if got := r.Header.Get("Authorization"); got != "Bearer github-access" {
				t.Fatalf("authorization=%q", got)
			}
			_, _ = w.Write([]byte(`{"token":"copilot-token","expires_at":4102444800}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldFactory := gitHubCopilotEndpointFactory
	oldSleep := gitHubCopilotPollSleep
	oldEnable := enableAllGitHubCopilotModelsFunc
	gitHubCopilotEndpointFactory = func(string) gitHubCopilotEndpoints {
		return gitHubCopilotEndpoints{
			DeviceCodeURL:   server.URL + "/login/device/code",
			AccessTokenURL:  server.URL + "/login/oauth/access_token",
			CopilotTokenURL: server.URL + "/copilot_internal/v2/token",
		}
	}
	gitHubCopilotPollSleep = func(context.Context, time.Duration) error { return nil }
	enableAllGitHubCopilotModelsFunc = func(context.Context, string, string, *http.Client, func(string)) {
		enabled = true
	}
	defer func() {
		gitHubCopilotEndpointFactory = oldFactory
		gitHubCopilotPollSleep = oldSleep
		enableAllGitHubCopilotModelsFunc = oldEnable
	}()

	var deviceInfo OAuthDeviceCodeInfo
	credentials, err := LoginGitHubCopilot(OAuthLoginCallbacks{
		OnPrompt: func(prompt OAuthPrompt) (string, error) {
			if !prompt.AllowEmpty {
				t.Fatalf("prompt=%#v", prompt)
			}
			return "", nil
		},
		OnDeviceCode: func(info OAuthDeviceCodeInfo) {
			deviceInfo = info
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deviceInfo.UserCode != "ABCD-EFGH" || credentials.Access != "copilot-token" || credentials.Refresh != "github-access" || !enabled {
		t.Fatalf("device=%#v credentials=%#v enabled=%v", deviceInfo, credentials, enabled)
	}
}

func TestRefreshTokenRequestShapes(t *testing.T) {
	var anthropicContentType string
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r2","expires_in":3600}`))
	}))
	defer anthropic.Close()
	if _, err := RefreshAnthropicToken(context.Background(), "r1", OAuthHTTPOptions{TokenURL: anthropic.URL}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(anthropicContentType, "application/json") {
		t.Fatalf("anthropic content-type=%q", anthropicContentType)
	}

	var openAIContentType string
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openAIContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"access_token":"` + codexTestJWT("acct-test") + `","refresh_token":"r2","expires_in":3600}`))
	}))
	defer openAI.Close()
	if _, err := RefreshOpenAICodexToken(context.Background(), "r1", OAuthHTTPOptions{TokenURL: openAI.URL}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(openAIContentType, "application/x-www-form-urlencoded") {
		t.Fatalf("openai content-type=%q", openAIContentType)
	}
}

func TestOpenAICodexDeviceAuthFlow(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"CODE-1","interval":"1"}`))
		case "/token":
			polls++
			w.Header().Set("Content-Type", "application/json")
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"deviceauth_authorization_pending"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_verifier":"verifier"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	endpoints := openaicodexauth.Endpoints{
		UserCodeURL:     server.URL + "/usercode",
		TokenURL:        server.URL + "/token",
		VerificationURI: server.URL + "/verify",
	}
	device, err := openaicodexauth.Start(context.Background(), server.Client(), openAICodexOAuthClientID, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceAuthID != "dev-1" || device.UserCode != "CODE-1" {
		t.Fatalf("device=%#v", device)
	}
	code, verifier, err := openaicodexauth.Poll(context.Background(), server.Client(), device, endpoints, 3)
	if err != nil {
		t.Fatal(err)
	}
	if code != "auth-code" || verifier != "verifier" || polls != 2 {
		t.Fatalf("code=%q verifier=%q polls=%d", code, verifier, polls)
	}
}

func TestPollOAuthDeviceCodeFlowPropagatesPollError(t *testing.T) {
	want := errors.New("network")
	_, err := PollOAuthDeviceCodeFlow(context.Background(), OAuthDeviceCodePollOptions{
		Sleep: func(context.Context, time.Duration) error { return nil },
		Poll:  func(context.Context) (OAuthDeviceCodePollResult, error) { return OAuthDeviceCodePollResult{}, want },
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func containsJSONField(raw []byte, field string) bool {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, ok := object[field]
	return ok
}
