package cautils

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestGetPiUserAgent(t *testing.T) {
	agent := GetPiUserAgent(" 1.2.3 ")
	if !strings.HasPrefix(agent, "pi/1.2.3 ") {
		t.Fatalf("user agent = %q", agent)
	}
	if !strings.Contains(agent, runtime.GOOS) || !strings.Contains(agent, runtime.GOARCH) {
		t.Fatalf("user agent missing platform: %q", agent)
	}
	if !strings.Contains(agent, "go/") {
		t.Fatalf("user agent missing go version: %q", agent)
	}
}

func TestComparePackageVersions(t *testing.T) {
	if comparison, ok := ComparePackageVersions("v1.2.3", "1.2.2"); !ok || comparison <= 0 {
		t.Fatalf("comparison=%d ok=%v", comparison, ok)
	}
	if comparison, ok := ComparePackageVersions("1.2.3-beta", "1.2.3"); !ok || comparison >= 0 {
		t.Fatalf("prerelease comparison=%d ok=%v", comparison, ok)
	}
	if !IsNewerPackageVersion("not-semver-a", "not-semver-b") {
		t.Fatal("expected fallback string comparison to report change")
	}
}

func TestGetLatestPiRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != GetPiUserAgent("1.0.0") {
			t.Fatalf("user agent = %q", got)
		}
		_, _ = w.Write([]byte(`{"version":" 9.8.7 ","packageName":"pi","note":"hi"}`))
	}))
	defer server.Close()

	release, err := GetLatestPiRelease(context.Background(), "1.0.0", VersionCheckOptions{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if release == nil || release.Version != "9.8.7" || release.PackageName != "pi" || release.Note != "hi" {
		t.Fatalf("release=%#v", release)
	}
}

func TestGetLatestPiReleaseHonorsOfflineEnv(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	release, err := GetLatestPiRelease(context.Background(), "1.0.0", VersionCheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if release != nil {
		t.Fatalf("release=%#v", release)
	}
}
