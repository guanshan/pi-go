package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAuthStorageSaveMergesConcurrentProviders verifies that saving a credential
// re-reads and merges the on-disk file, so a credential written by another
// process (simulated by an out-of-band file write) is not clobbered.
func TestAuthStorageSaveMergesConcurrentProviders(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	if err := auth.SaveAPIKey("anthropic", "anthropic-key"); err != nil {
		t.Fatal(err)
	}

	// Simulate another pi process storing a different provider directly.
	path := filepath.Join(dir, "auth.json")
	var disk map[string]json.RawMessage
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	disk["openai"] = json.RawMessage(`{"type":"api_key","key":"openai-key"}`)
	merged, _ := json.Marshal(disk)
	if err := os.WriteFile(path, merged, 0o600); err != nil {
		t.Fatal(err)
	}

	// This process saves yet another provider; it must not drop "openai".
	if err := auth.SaveAPIKey("google", "google-key"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewAuthStorage(dir)
	for provider, want := range map[string]string{
		"anthropic": "anthropic-key",
		"openai":    "openai-key",
		"google":    "google-key",
	} {
		if got := reloaded.APIKey(Model{Provider: provider}); got != want {
			t.Fatalf("provider %q = %q, want %q (concurrent write was clobbered)", provider, got, want)
		}
	}
}

// TestAuthStorageConcurrentSavesNoDataLoss runs many concurrent SaveAPIKey calls
// (which would panic on concurrent map writes or lose entries without the lock).
func TestAuthStorageConcurrentSavesNoDataLoss(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			provider := "provider-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			if err := auth.SaveAPIKey(provider, provider+"-key"); err != nil {
				t.Errorf("save %s: %v", provider, err)
			}
		}(i)
	}
	wg.Wait()

	reloaded := NewAuthStorage(dir)
	if got := len(reloaded.List()); got != n {
		t.Fatalf("persisted %d providers, want %d", got, n)
	}
}

// TestRefreshOAuthCredentialsSingleflight verifies that when many goroutines hit
// an expired OAuth token at once, the locked refresh runs once and every caller
// observes the refreshed credentials.
func TestRefreshOAuthCredentialsSingleflight(t *testing.T) {
	dir := t.TempDir()
	auth := NewAuthStorage(dir)
	if err := auth.SaveOAuth("test", OAuthCredentials{
		Access:  "old",
		Refresh: "refresh",
		Expires: time.Now().Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	var refreshes int64
	refresh := func(current OAuthCredentials) (OAuthCredentials, error) {
		atomic.AddInt64(&refreshes, 1)
		return OAuthCredentials{
			Access:  "new",
			Refresh: "refresh",
			Expires: time.Now().Add(time.Hour).UnixMilli(),
		}, nil
	}

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			creds, ok, err := auth.RefreshOAuthCredentials("test", refresh)
			if err != nil {
				t.Errorf("refresh: %v", err)
				return
			}
			if !ok || creds.Access != "new" {
				t.Errorf("got creds=%#v ok=%v, want access=new", creds, ok)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&refreshes); got != 1 {
		t.Fatalf("refresh ran %d times, want exactly 1 (singleflight)", got)
	}
	reloaded := NewAuthStorage(dir)
	if got := reloaded.APIKey(Model{Provider: "test"}); got != "new" {
		t.Fatalf("persisted access = %q, want new", got)
	}
}
