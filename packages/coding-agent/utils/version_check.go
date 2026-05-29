package cautils

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	LatestVersionURL             = "https://pi.dev/api/latest-version"
	DefaultVersionCheckTimeoutMS = 10000
)

type LatestPiRelease struct {
	Version     string `json:"version"`
	PackageName string `json:"packageName,omitempty"`
	Note        string `json:"note,omitempty"`
}

type VersionCheckOptions struct {
	Timeout time.Duration
	URL     string
	Client  *http.Client
}

type parsedPackageVersion struct {
	major      int
	minor      int
	patch      int
	prerelease string
}

func ComparePackageVersions(leftVersion, rightVersion string) (int, bool) {
	left, ok := parsePackageVersion(leftVersion)
	if !ok {
		return 0, false
	}
	right, ok := parsePackageVersion(rightVersion)
	if !ok {
		return 0, false
	}
	if left.major != right.major {
		return left.major - right.major, true
	}
	if left.minor != right.minor {
		return left.minor - right.minor, true
	}
	if left.patch != right.patch {
		return left.patch - right.patch, true
	}
	if left.prerelease == right.prerelease {
		return 0, true
	}
	if left.prerelease == "" {
		return 1, true
	}
	if right.prerelease == "" {
		return -1, true
	}
	return strings.Compare(left.prerelease, right.prerelease), true
}

func IsNewerPackageVersion(candidateVersion, currentVersion string) bool {
	if comparison, ok := ComparePackageVersions(candidateVersion, currentVersion); ok {
		return comparison > 0
	}
	return strings.TrimSpace(candidateVersion) != strings.TrimSpace(currentVersion)
}

func GetLatestPiRelease(ctx context.Context, currentVersion string, options VersionCheckOptions) (*LatestPiRelease, error) {
	if os.Getenv("PI_SKIP_VERSION_CHECK") != "" || os.Getenv("PI_OFFLINE") != "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = time.Duration(DefaultVersionCheckTimeoutMS) * time.Millisecond
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	endpoint := options.URL
	if endpoint == "" {
		endpoint = LatestVersionURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", GetPiUserAgent(currentVersion))
	req.Header.Set("Accept", "application/json")
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil
	}
	var payload struct {
		Version     any `json:"version"`
		PackageName any `json:"packageName"`
		Note        any `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	version, ok := payload.Version.(string)
	if !ok || strings.TrimSpace(version) == "" {
		return nil, nil
	}
	release := &LatestPiRelease{Version: strings.TrimSpace(version)}
	if packageName, ok := payload.PackageName.(string); ok && strings.TrimSpace(packageName) != "" {
		release.PackageName = strings.TrimSpace(packageName)
	}
	if note, ok := payload.Note.(string); ok && strings.TrimSpace(note) != "" {
		release.Note = strings.TrimSpace(note)
	}
	return release, nil
}

func GetLatestPiVersion(ctx context.Context, currentVersion string, options VersionCheckOptions) (string, error) {
	release, err := GetLatestPiRelease(ctx, currentVersion, options)
	if err != nil || release == nil {
		return "", err
	}
	return release.Version, nil
}

func CheckForNewPiVersion(ctx context.Context, currentVersion string) (*LatestPiRelease, error) {
	release, err := GetLatestPiRelease(ctx, currentVersion, VersionCheckOptions{})
	if err != nil || release == nil {
		return nil, nil
	}
	if IsNewerPackageVersion(release.Version, currentVersion) {
		return release, nil
	}
	return nil, nil
}

func parsePackageVersion(version string) (parsedPackageVersion, bool) {
	value := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	parts := strings.SplitN(value, "+", 2)
	value = parts[0]
	coreAndPre := strings.SplitN(value, "-", 2)
	core := strings.Split(coreAndPre[0], ".")
	if len(core) != 3 {
		return parsedPackageVersion{}, false
	}
	major, err := strconv.Atoi(core[0])
	if err != nil {
		return parsedPackageVersion{}, false
	}
	minor, err := strconv.Atoi(core[1])
	if err != nil {
		return parsedPackageVersion{}, false
	}
	patch, err := strconv.Atoi(core[2])
	if err != nil {
		return parsedPackageVersion{}, false
	}
	out := parsedPackageVersion{major: major, minor: minor, patch: patch}
	if len(coreAndPre) == 2 {
		out.prerelease = coreAndPre[1]
	}
	return out, true
}
