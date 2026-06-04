package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseTokenResponseComputesExpiry(t *testing.T) {
	raw := []byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`)
	before := time.Now()
	creds, err := ParseTokenResponse(raw, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "a" || creds.Refresh != "r" {
		t.Fatalf("creds=%#v", creds)
	}
	// expires_in 3600s minus 60s slack => roughly now+3540s.
	wantMin := before.Add(3540*time.Second - time.Second).UnixMilli()
	wantMax := time.Now().Add(3540 * time.Second).UnixMilli()
	if creds.Expires < wantMin || creds.Expires > wantMax {
		t.Fatalf("expires=%d not in [%d,%d]", creds.Expires, wantMin, wantMax)
	}
}

func TestParseTokenResponseMissingFields(t *testing.T) {
	for _, raw := range []string{
		`{"refresh_token":"r","expires_in":1}`,     // no access_token
		`{"access_token":"a","expires_in":1}`,      // no refresh_token
		`{"access_token":"a","refresh_token":"r"}`, // no expires_in
	} {
		if _, err := ParseTokenResponse([]byte(raw), 0); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestRefreshFormTokenUsesInjectedClient(t *testing.T) {
	var capturedForm url.Values
	var capturedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		capturedForm, _ = url.ParseQuery(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    1800,
		})
	}))
	defer server.Close()

	creds, err := RefreshFormToken(context.Background(), "old-refresh", server.URL, "client-xyz", 0, HTTPOptions{Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if creds.Access != "new-access" || creds.Refresh != "new-refresh" {
		t.Fatalf("creds=%#v", creds)
	}
	if capturedForm.Get("grant_type") != "refresh_token" ||
		capturedForm.Get("refresh_token") != "old-refresh" ||
		capturedForm.Get("client_id") != "client-xyz" {
		t.Fatalf("form=%v", capturedForm)
	}
	if !strings.HasPrefix(capturedContentType, "application/x-www-form-urlencoded") {
		t.Fatalf("content-type=%q", capturedContentType)
	}
}

func TestRefreshFormTokenTokenURLOverride(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "a",
			"refresh_token": "r",
			"expires_in":    10,
		})
	}))
	defer server.Close()

	// The TokenURL in HTTPOptions must override the positional tokenURL argument.
	_, err := RefreshFormToken(context.Background(), "ref", "http://unused.invalid", "cid", 0,
		HTTPOptions{Client: server.Client(), TokenURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("expected override TokenURL to be used")
	}
}

func TestRefreshJSONTokenSendsJSONBody(t *testing.T) {
	var captured map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type=%q, want application/json", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "a",
			"refresh_token": "r",
			"expires_in":    10,
		})
	}))
	defer server.Close()

	if _, err := RefreshJSONToken(context.Background(), "ref", server.URL, "cid", 0, HTTPOptions{Client: server.Client()}); err != nil {
		t.Fatal(err)
	}
	if captured["grant_type"] != "refresh_token" || captured["refresh_token"] != "ref" || captured["client_id"] != "cid" {
		t.Fatalf("captured=%v", captured)
	}
}

func TestRequestFormTokenNon2xxError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid_grant"))
	}))
	defer server.Close()

	_, err := RefreshFormToken(context.Background(), "ref", server.URL, "cid", 0, HTTPOptions{Client: server.Client()})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err=%v, want invalid_grant", err)
	}
}

func TestStringExtra(t *testing.T) {
	creds := Credentials{Extra: map[string]any{"account_id": "acct-1", "n": 5}}
	if got := StringExtra(creds, "account_id"); got != "acct-1" {
		t.Fatalf("StringExtra=%q", got)
	}
	if got := StringExtra(creds, "n"); got != "" {
		t.Fatalf("StringExtra(non-string)=%q, want empty", got)
	}
	if got := StringExtra(Credentials{}, "x"); got != "" {
		t.Fatalf("StringExtra(nil extra)=%q", got)
	}
}
