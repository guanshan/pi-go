package openaicodexauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStartParsesDeviceAuth(t *testing.T) {
	var capturedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_auth_id": "dev-1",
			"user_code":      "ABCD-EFGH",
			"interval":       5,
		})
	}))
	defer server.Close()

	auth, err := Start(context.Background(), server.Client(), "client-123", Endpoints{UserCodeURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if auth.DeviceAuthID != "dev-1" || auth.UserCode != "ABCD-EFGH" || auth.IntervalSeconds != 5 {
		t.Fatalf("auth=%#v", auth)
	}
	if capturedBody["client_id"] != "client-123" {
		t.Fatalf("client_id=%q", capturedBody["client_id"])
	}
}

func TestStartNotFoundGivesFriendlyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := Start(context.Background(), server.Client(), "cid", Endpoints{UserCodeURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "device code login is not enabled") {
		t.Fatalf("err=%v, want friendly not-enabled message", err)
	}
}

func TestStartRejectsInvalidPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user_code":"x","interval":5}`)) // missing device_auth_id
	}))
	defer server.Close()

	_, err := Start(context.Background(), server.Client(), "cid", Endpoints{UserCodeURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid OpenAI Codex device code response") {
		t.Fatalf("err=%v, want invalid-response error", err)
	}
}

func TestPollPendingThenComplete(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusForbidden) // treated as pending
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_code": "auth-code-9",
			"code_verifier":      "verifier-9",
		})
	}))
	defer server.Close()

	device := DeviceAuth{DeviceAuthID: "dev-1", UserCode: "X", IntervalSeconds: 0.001}
	code, verifier, err := Poll(context.Background(), server.Client(), device, Endpoints{TokenURL: server.URL}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if code != "auth-code-9" || verifier != "verifier-9" {
		t.Fatalf("code=%q verifier=%q", code, verifier)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected at least 2 poll calls, got %d", calls)
	}
}

func TestPollAuthorizationPendingErrorCode(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"deviceauth_authorization_pending"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_code": "c",
			"code_verifier":      "v",
		})
	}))
	defer server.Close()

	device := DeviceAuth{DeviceAuthID: "d", UserCode: "X", IntervalSeconds: 0.001}
	code, verifier, err := Poll(context.Background(), server.Client(), device, Endpoints{TokenURL: server.URL}, 60)
	if err != nil {
		t.Fatal(err)
	}
	if code != "c" || verifier != "v" {
		t.Fatalf("code=%q verifier=%q", code, verifier)
	}
}

func TestNormalizeEndpointsFillsDefaults(t *testing.T) {
	got := normalizeEndpoints(Endpoints{})
	if got.UserCodeURL != DefaultUserCodeURL || got.TokenURL != DefaultTokenURL || got.VerificationURI != DefaultVerificationURI {
		t.Fatalf("normalizeEndpoints defaults=%#v", got)
	}
}

func TestDeviceErrorCodeShapes(t *testing.T) {
	if got := deviceErrorCode([]byte(`{"error":"slow_down"}`)); got != "slow_down" {
		t.Fatalf("string error code=%q", got)
	}
	if got := deviceErrorCode([]byte(`{"error":{"code":"x"}}`)); got != "x" {
		t.Fatalf("object error code=%q", got)
	}
	if got := deviceErrorCode([]byte(`not json`)); got != "" {
		t.Fatalf("invalid json error code=%q", got)
	}
}
