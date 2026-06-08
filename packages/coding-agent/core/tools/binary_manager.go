package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	managedToolFD = "fd"
	managedToolRG = "rg"

	managedToolNetworkTimeout  = 10 * time.Second
	managedToolDownloadTimeout = 120 * time.Second
	managedToolFailureTTL      = 5 * time.Minute
	managedToolLockPoll        = 100 * time.Millisecond
	managedToolLockStaleAfter  = 10 * time.Minute
)

type managedToolConfig struct {
	Key         string
	Name        string
	Repo        string
	BinaryName  string
	TagPrefix   string
	SystemNames []string
}

type managedToolResult struct {
	Path       string
	Diagnostic string
	Downloaded bool
}

var managedTools = map[string]managedToolConfig{
	managedToolFD: {
		Key:         managedToolFD,
		Name:        "fd",
		Repo:        "sharkdp/fd",
		BinaryName:  "fd",
		TagPrefix:   "v",
		SystemNames: []string{"fd", "fdfind"},
	},
	managedToolRG: {
		Key:         managedToolRG,
		Name:        "ripgrep",
		Repo:        "BurntSushi/ripgrep",
		BinaryName:  "rg",
		SystemNames: []string{"rg"},
	},
}

var (
	managedToolLookPath       = exec.LookPath
	managedToolNetworkClient  = &http.Client{Timeout: managedToolNetworkTimeout}
	managedToolDownloadClient = &http.Client{Timeout: managedToolDownloadTimeout}
	managedToolDownloader     = downloadManagedTool
	managedToolInstallState   = newManagedToolInstallState()
)

type managedToolCachedFailure struct {
	Diagnostic string
	ExpiresAt  time.Time
}

type managedToolInstallStateData struct {
	mu       sync.Mutex
	wg       sync.WaitGroup
	inFlight map[string]bool
	failures map[string]managedToolCachedFailure
}

func newManagedToolInstallState() *managedToolInstallStateData {
	return &managedToolInstallStateData{
		inFlight: map[string]bool{},
		failures: map[string]managedToolCachedFailure{},
	}
}

func ensureManagedTool(ctx context.Context, key, binDir string) managedToolResult {
	if ctx == nil {
		ctx = context.Background()
	}
	config, ok := managedTools[key]
	if !ok {
		return managedToolResult{Diagnostic: fmt.Sprintf("Unknown managed tool: %s", key)}
	}
	if path, ok := localManagedToolPath(binDir, config); ok {
		return managedToolResult{Path: path}
	}
	if path, ok := systemManagedToolPath(config); ok {
		return managedToolResult{Path: path}
	}
	if binDir == "" {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found and no agent bin directory is configured", config.Name)}
	}
	if isManagedToolOfflineMode() {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found. Offline mode enabled, skipping download", config.Name)}
	}
	if runtime.GOOS == "android" {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found. Install with: pkg install %s", config.Name, termuxPackageName(key))}
	}
	path, err := installManagedToolWithLock(ctx, config, binDir)
	if err != nil {
		return managedToolResult{Diagnostic: fmt.Sprintf("Failed to download %s: %v", config.Name, err)}
	}
	return managedToolResult{Path: path, Downloaded: true}
}

func resolveManagedTool(_ context.Context, key, binDir string) managedToolResult {
	config, ok := managedTools[key]
	if !ok {
		return managedToolResult{Diagnostic: fmt.Sprintf("Unknown managed tool: %s", key)}
	}
	if path, ok := localManagedToolPath(binDir, config); ok {
		return managedToolResult{Path: path}
	}
	if path, ok := systemManagedToolPath(config); ok {
		return managedToolResult{Path: path}
	}
	if binDir == "" {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found and no agent bin directory is configured", config.Name)}
	}
	if isManagedToolOfflineMode() {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found. Offline mode enabled, skipping download", config.Name)}
	}
	if runtime.GOOS == "android" {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found. Install with: pkg install %s", config.Name, termuxPackageName(key))}
	}
	cacheKey := managedToolCacheKey(key, binDir)
	if diagnostic, ok := managedToolCachedFailureDiagnostic(cacheKey); ok {
		return managedToolResult{Diagnostic: diagnostic}
	}
	if startManagedToolBackgroundInstall(cacheKey, config, binDir) {
		return managedToolResult{Diagnostic: fmt.Sprintf("%s not found; background download started", config.Name)}
	}
	return managedToolResult{Diagnostic: fmt.Sprintf("%s not found; background download already in progress", config.Name)}
}

func managedToolCacheKey(key, binDir string) string {
	return key + "\x00" + binDir
}

func managedToolCachedFailureDiagnostic(cacheKey string) (string, bool) {
	state := managedToolInstallState
	state.mu.Lock()
	defer state.mu.Unlock()
	cached, ok := state.failures[cacheKey]
	if !ok {
		return "", false
	}
	if time.Now().After(cached.ExpiresAt) {
		delete(state.failures, cacheKey)
		return "", false
	}
	return cached.Diagnostic, true
}

func startManagedToolBackgroundInstall(cacheKey string, config managedToolConfig, binDir string) bool {
	state := managedToolInstallState
	state.mu.Lock()
	if state.inFlight[cacheKey] {
		state.mu.Unlock()
		return false
	}
	state.inFlight[cacheKey] = true
	state.wg.Add(1)
	state.mu.Unlock()

	go func() {
		defer state.wg.Done()
		_, err := installManagedToolWithLock(context.Background(), config, binDir)
		state.mu.Lock()
		defer state.mu.Unlock()
		delete(state.inFlight, cacheKey)
		if err != nil {
			state.failures[cacheKey] = managedToolCachedFailure{
				Diagnostic: fmt.Sprintf("Failed to download %s: %v", config.Name, err),
				ExpiresAt:  time.Now().Add(managedToolFailureTTL),
			}
			return
		}
		delete(state.failures, cacheKey)
	}()
	return true
}

func localManagedToolPath(binDir string, config managedToolConfig) (string, bool) {
	if binDir == "" {
		return "", false
	}
	candidate := filepath.Join(binDir, managedToolBinaryFileName(config))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, true
	}
	return "", false
}

func systemManagedToolPath(config managedToolConfig) (string, bool) {
	names := config.SystemNames
	if len(names) == 0 {
		names = []string{config.BinaryName}
	}
	for _, name := range names {
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
			name += ".exe"
		}
		if path, err := managedToolLookPath(name); err == nil && path != "" {
			return path, true
		}
	}
	return "", false
}

func installManagedToolWithLock(ctx context.Context, config managedToolConfig, binDir string) (string, error) {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	lockPath := filepath.Join(binDir, "."+config.BinaryName+".download.lock")
	lock, err := acquireManagedToolLock(ctx, lockPath)
	if err != nil {
		return "", err
	}
	defer releaseManagedToolLock(lock)
	if path, ok := localManagedToolPath(binDir, config); ok {
		return path, nil
	}
	if path, ok := systemManagedToolPath(config); ok {
		return path, nil
	}
	return managedToolDownloader(ctx, config, binDir)
}

type managedToolLock struct {
	file  *os.File
	path  string
	token string
}

func acquireManagedToolLock(ctx context.Context, lockPath string) (*managedToolLock, error) {
	for {
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			token := managedToolLockToken()
			_, _ = fmt.Fprintf(file, "pid=%d\ncreated=%s\ntoken=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339), token)
			return &managedToolLock{file: file, path: lockPath, token: token}, nil
		}
		if !isManagedToolLockContention(err, lockPath) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > managedToolLockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(managedToolLockPoll):
		}
	}
}

func isManagedToolLockContention(err error, lockPath string) bool {
	if os.IsExist(err) {
		return true
	}
	if runtime.GOOS != "windows" || !os.IsPermission(err) {
		return false
	}
	info, statErr := os.Stat(lockPath)
	return statErr == nil && !info.IsDir()
}

func releaseManagedToolLock(lock *managedToolLock) {
	if lock == nil {
		return
	}
	if lock.file != nil {
		_ = lock.file.Close()
	}
	if lock.path == "" {
		return
	}
	if data, err := os.ReadFile(lock.path); err == nil && !strings.Contains(string(data), "token="+lock.token) {
		return
	}
	_ = os.Remove(lock.path)
}

func managedToolLockToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

func downloadManagedTool(ctx context.Context, config managedToolConfig, binDir string) (string, error) {
	release, err := fetchManagedToolRelease(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", config.Repo))
	if err != nil {
		return "", err
	}
	version := release.Version
	if version == "" {
		return "", errors.New("GitHub latest release did not include tag_name")
	}
	digests := release.Digests
	if config.Key == managedToolFD && runtime.GOOS == "darwin" && runtime.GOARCH == "amd64" {
		version = "10.3.0"
		// The latest-release digests do not describe the pinned version's
		// assets; fetch the pinned release's digests (best effort).
		digests = nil
		if pinned, perr := fetchManagedToolRelease(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s%s", config.Repo, config.TagPrefix, version)); perr == nil {
			digests = pinned.Digests
		}
	}
	assetName, err := managedToolAssetName(config, version)
	if err != nil {
		return "", err
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s%s/%s", config.Repo, config.TagPrefix, version, assetName)
	archivePath, err := downloadManagedToolArchive(ctx, downloadURL, binDir, assetName)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	// Verify integrity when GitHub publishes a digest for the asset. Older
	// releases may not carry one; in that case we proceed unverified rather
	// than disable the accelerator entirely (TLS to GitHub is the only trust
	// anchor available then).
	if expected := digests[assetName]; expected != "" {
		if err := verifyManagedToolArchiveDigest(archivePath, expected); err != nil {
			return "", err
		}
	}

	extractDir, err := os.MkdirTemp(binDir, ".extract_"+config.BinaryName+"_")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(extractDir)

	if err := extractManagedToolArchive(archivePath, extractDir, assetName); err != nil {
		return "", err
	}
	binaryFileName := managedToolBinaryFileName(config)
	extractedBinary, err := findManagedToolBinary(extractDir, binaryFileName)
	if err != nil {
		return "", err
	}
	finalPath := filepath.Join(binDir, binaryFileName)
	tmpFinal, err := tempManagedToolPath(binDir, binaryFileName)
	if err != nil {
		return "", err
	}
	if err := os.Rename(extractedBinary, tmpFinal); err != nil {
		_ = os.Remove(tmpFinal)
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpFinal, 0o755); err != nil {
			_ = os.Remove(tmpFinal)
			return "", err
		}
	}
	if err := os.Rename(tmpFinal, finalPath); err != nil {
		_ = os.Remove(tmpFinal)
		return "", err
	}
	return finalPath, nil
}

type managedToolReleaseInfo struct {
	Version string
	Digests map[string]string // asset name -> digest (e.g. "sha256:abcd…"); may be empty
}

func fetchManagedToolRelease(ctx context.Context, endpoint string) (managedToolReleaseInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return managedToolReleaseInfo{}, err
	}
	request.Header.Set("User-Agent", "pi-coding-agent")
	response, err := managedToolNetworkClient.Do(request)
	if err != nil {
		return managedToolReleaseInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return managedToolReleaseInfo{}, fmt.Errorf("GitHub API error: %d", response.StatusCode)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return managedToolReleaseInfo{}, err
	}
	info := managedToolReleaseInfo{Version: strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")}
	for _, asset := range payload.Assets {
		if asset.Digest != "" {
			if info.Digests == nil {
				info.Digests = make(map[string]string, len(payload.Assets))
			}
			info.Digests[asset.Name] = asset.Digest
		}
	}
	return info, nil
}

// verifyManagedToolArchiveDigest checks the downloaded archive against a digest
// of the form "sha256:<hex>". Unknown algorithms are treated as unverifiable
// (skipped) rather than rejected, but a sha256 mismatch fails closed.
func verifyManagedToolArchiveDigest(path, expected string) error {
	algo, want, ok := strings.Cut(expected, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(algo), "sha256") {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("checksum mismatch for %s: expected sha256:%s, got sha256:%s", filepath.Base(path), strings.TrimSpace(want), got)
	}
	return nil
}

func downloadManagedToolArchive(ctx context.Context, url, binDir, assetName string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := managedToolDownloadClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("failed to download: %d", response.StatusCode)
	}
	file, err := os.CreateTemp(binDir, assetName+".")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	return path, nil
}

func managedToolAssetName(config managedToolConfig, version string) (string, error) {
	arch, err := managedToolReleaseArch()
	if err != nil {
		return "", err
	}
	switch config.Key {
	case managedToolFD:
		switch runtime.GOOS {
		case "darwin":
			return fmt.Sprintf("fd-v%s-%s-apple-darwin.tar.gz", version, arch), nil
		case "linux":
			return fmt.Sprintf("fd-v%s-%s-unknown-linux-gnu.tar.gz", version, arch), nil
		case "windows":
			return fmt.Sprintf("fd-v%s-%s-pc-windows-msvc.zip", version, arch), nil
		}
	case managedToolRG:
		switch runtime.GOOS {
		case "darwin":
			return fmt.Sprintf("ripgrep-%s-%s-apple-darwin.tar.gz", version, arch), nil
		case "linux":
			if runtime.GOARCH == "arm64" {
				return fmt.Sprintf("ripgrep-%s-aarch64-unknown-linux-gnu.tar.gz", version), nil
			}
			return fmt.Sprintf("ripgrep-%s-x86_64-unknown-linux-musl.tar.gz", version), nil
		case "windows":
			return fmt.Sprintf("ripgrep-%s-%s-pc-windows-msvc.zip", version, arch), nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
}

func managedToolReleaseArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

func extractManagedToolArchive(archivePath, extractDir, assetName string) error {
	if strings.HasSuffix(assetName, ".tar.gz") {
		return extractManagedToolTarGz(archivePath, extractDir)
	}
	if strings.HasSuffix(assetName, ".zip") {
		return extractManagedToolZip(archivePath, extractDir)
	}
	return fmt.Errorf("unsupported archive format: %s", assetName)
}

func extractManagedToolTarGz(archivePath, extractDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeManagedToolExtractPath(extractDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		// The tar Reader normalizes the deprecated TypeRegA to TypeReg on read, so
		// matching TypeReg alone covers regular files from legacy GNU tar archives.
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, reader)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func extractManagedToolZip(archivePath, extractDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		target, err := safeManagedToolExtractPath(extractDir, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, entry.Mode()&0o777)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeInErr := in.Close()
		closeOutErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
	}
	return nil
}

func safeManagedToolExtractPath(root, name string) (string, error) {
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || cleanName == ".." {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	target := filepath.Join(root, cleanName)
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if cleanTarget != cleanRoot && !strings.HasPrefix(cleanTarget, cleanRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return target, nil
}

func findManagedToolBinary(root, binaryFileName string) (string, error) {
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				stack = append(stack, path)
				continue
			}
			if entry.Type().IsRegular() && entry.Name() == binaryFileName {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("binary not found in archive: expected %s under %s", binaryFileName, root)
}

func tempManagedToolPath(binDir, binaryFileName string) (string, error) {
	file, err := os.CreateTemp(binDir, "."+binaryFileName+".")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func managedToolBinaryFileName(config managedToolConfig) string {
	if runtime.GOOS == "windows" {
		return config.BinaryName + ".exe"
	}
	return config.BinaryName
}

func isManagedToolOfflineMode() bool {
	value := strings.TrimSpace(os.Getenv("PI_OFFLINE"))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func termuxPackageName(key string) string {
	switch key {
	case managedToolRG:
		return "ripgrep"
	default:
		return key
	}
}
