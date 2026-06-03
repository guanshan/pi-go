package ai

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRoundTripper records whether it was invoked and serves canned responses
// keyed by request URL substring, so OAuth login flows can run without touching
// the real network.
type fakeRoundTripper struct {
	called    atomic.Int32
	lastURL   atomic.Value // string
	responder func(*http.Request) (*http.Response, error)
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	f.called.Add(1)
	f.lastURL.Store(req.URL.String())
	return f.responder(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestLoginGitHubCopilotUsesInjectedClient covers P2-04: LoginGitHubCopilot must
// use the *http.Client provided via OAuthLoginCallbacks.HTTPClient instead of the
// hardcoded http.DefaultClient, so tests can stub the transport. The fake serves
// a valid device-code response and then a refusal on the poll, which is enough to
// prove the injected client carried the device-flow request.
func TestLoginGitHubCopilotUsesInjectedClient(t *testing.T) {
	fake := &fakeRoundTripper{}
	fake.responder = func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(req.URL.Path, "device/code"):
			return jsonResponse(200, `{"device_code":"dc","user_code":"UC","verification_uri":"https://example.test/verify","interval":0,"expires_in":900}`), nil
		default:
			// Returning an error here halts the flow after the device-code step;
			// the assertion only needs to confirm the injected client was used.
			return jsonResponse(400, `{"error":"authorization_pending"}`), nil
		}
	}

	injected := &http.Client{Transport: fake}
	_, err := LoginGitHubCopilot(OAuthLoginCallbacks{HTTPClient: injected})
	// The flow is expected to fail (no real authorization), but it must have used
	// the injected transport rather than http.DefaultClient.
	if err == nil {
		t.Log("login unexpectedly succeeded against fake transport")
	}
	if fake.called.Load() == 0 {
		t.Fatal("injected client was not used by LoginGitHubCopilot (still hardcoding http.DefaultClient?)")
	}
	if got, _ := fake.lastURL.Load().(string); !strings.Contains(got, "github.com") {
		t.Fatalf("unexpected request URL via injected client: %q", got)
	}
}

// TestLoginOpenAICodexDeviceCodeUsesInjectedClient covers the codex device-code
// login path: the injected client must carry the user-code request.
func TestLoginOpenAICodexDeviceCodeUsesInjectedClient(t *testing.T) {
	fake := &fakeRoundTripper{}
	fake.responder = func(req *http.Request) (*http.Response, error) {
		// Returning a non-2xx here stops the flow at the Start() step; we only
		// need to verify the injected transport was invoked.
		return jsonResponse(400, `{"error":"bad_request"}`), nil
	}

	injected := &http.Client{Transport: fake}
	_, err := LoginOpenAICodexDeviceCode(OAuthLoginCallbacks{HTTPClient: injected})
	if err == nil {
		t.Log("codex device-code login unexpectedly succeeded against fake transport")
	}
	if fake.called.Load() == 0 {
		t.Fatal("injected client was not used by LoginOpenAICodexDeviceCode (still hardcoding http.DefaultClient?)")
	}
}

// TestOAuthCallbacksHTTPClientDefault confirms the helper falls back to
// http.DefaultClient when no client is injected.
func TestOAuthCallbacksHTTPClientDefault(t *testing.T) {
	if got := (OAuthLoginCallbacks{}).httpClient(); got != http.DefaultClient {
		t.Fatalf("expected http.DefaultClient default, got %p", got)
	}
	custom := &http.Client{}
	if got := (OAuthLoginCallbacks{HTTPClient: custom}).httpClient(); got != custom {
		t.Fatalf("expected injected client, got %p", got)
	}
}
