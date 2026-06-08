package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

type clipboardImage struct {
	Bytes    []byte
	MimeType string
}

type clipboardCommandResult struct {
	Stdout []byte
	OK     bool
}

type clipboardCommandRunner func(command string, args []string, timeout time.Duration) clipboardCommandResult

const (
	clipboardListTimeout  = time.Second
	clipboardReadTimeout  = 3 * time.Second
	clipboardPowerTimeout = 5 * time.Second
)

var defaultClipboardCommandRunner clipboardCommandRunner = runClipboardImageCommand

func (m *interactiveModel) handleClipboardImagePaste() {
	image, err := readClipboardImageWithEnv(runtime.GOOS, os.Environ(), defaultClipboardCommandRunner)
	if err != nil {
		m.setStatus(err.Error())
		return
	}
	if image == nil {
		m.setStatus("No clipboard image found or clipboard image paste is unsupported.")
		return
	}
	path, err := writeClipboardImageTempFile(*image)
	if err != nil {
		m.appendMessage(interactiveRoleError, err.Error())
		return
	}
	insert := path
	if shouldPrependSpaceForPastedPath(insert, m.charBeforeInputCursor()) {
		insert = " " + insert
	}
	m.input.InsertString(insert)
	m.inputImages = append(m.inputImages, ai.ContentBlock{
		Type:     "image",
		MimeType: image.MimeType,
		Data:     base64.StdEncoding.EncodeToString(image.Bytes),
	})
	m.historyIndex = -1
	m.autocompleteIndex = 0
	m.setStatus("Pasted clipboard image.")
}

func readClipboardImageWithEnv(platform string, env []string, runner clipboardCommandRunner) (*clipboardImage, error) {
	if runner == nil {
		runner = defaultClipboardCommandRunner
	}
	envMap := envSliceToMap(env)
	if envMap["TERMUX_VERSION"] != "" {
		return nil, fmt.Errorf("clipboard image paste is unsupported in Termux")
	}
	switch platform {
	case "linux":
		wsl := isWSLEnv(envMap)
		wayland := isWaylandEnv(envMap)
		var image *clipboardImage
		if wayland || wsl {
			image = readClipboardImageViaWlPaste(runner)
			if image == nil {
				image = readClipboardImageViaXclip(runner)
			}
		}
		if image == nil && wsl {
			image = readClipboardImageViaPowerShell(runner)
		}
		if image == nil && !wayland {
			image = readClipboardImageViaXclip(runner)
		}
		return image, nil
	case "windows":
		return readClipboardImageViaWindowsPowerShell(runner), nil
	case "darwin":
		// Prefer pngpaste when installed (clean PNG on stdout); otherwise fall back
		// to built-in osascript, which covers the common screenshot case.
		if image := readClipboardImageViaPngpaste(runner); image != nil {
			return image, nil
		}
		return readClipboardImageViaOsascript(runner), nil
	default:
		return nil, fmt.Errorf("clipboard image paste is unsupported on %s", platform)
	}
}

func readClipboardImageViaPngpaste(runner clipboardCommandRunner) *clipboardImage {
	data := runner("pngpaste", []string{"-"}, clipboardReadTimeout)
	if !data.OK || len(data.Stdout) == 0 {
		return nil
	}
	return &clipboardImage{Bytes: data.Stdout, MimeType: "image/png"}
}

func readClipboardImageViaOsascript(runner clipboardCommandRunner) *clipboardImage {
	tmp, err := os.CreateTemp("", "pi-mac-clip-*.png")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	result := runner("osascript", clipboardOsascriptArgs(tmpPath), clipboardPowerTimeout)
	if !result.OK || strings.TrimSpace(string(result.Stdout)) != "ok" {
		return nil
	}
	bytes, err := os.ReadFile(tmpPath)
	if err != nil || len(bytes) == 0 {
		return nil
	}
	return &clipboardImage{Bytes: bytes, MimeType: "image/png"}
}

func clipboardOsascriptArgs(path string) []string {
	quoted := strings.ReplaceAll(path, "\"", "\\\"")
	script := strings.Join([]string{
		"set outFile to POSIX file \"" + quoted + "\"",
		"try",
		"\tset imageData to (the clipboard as «class PNGf»)",
		"on error",
		"\treturn \"empty\"",
		"end try",
		"set fileRef to (open for access outFile with write permission)",
		"set eof fileRef to 0",
		"write imageData to fileRef",
		"close access fileRef",
		"return \"ok\"",
	}, "\n")
	return []string{"-e", script}
}

func readClipboardImageViaWlPaste(runner clipboardCommandRunner) *clipboardImage {
	list := runner("wl-paste", []string{"--list-types"}, clipboardListTimeout)
	if !list.OK {
		return nil
	}
	selected := selectPreferredImageMimeType(splitClipboardMimeTypes(list.Stdout))
	if selected == "" {
		return nil
	}
	data := runner("wl-paste", []string{"--type", selected, "--no-newline"}, clipboardReadTimeout)
	if !data.OK || len(data.Stdout) == 0 {
		return nil
	}
	return &clipboardImage{Bytes: data.Stdout, MimeType: baseMimeType(selected)}
}

func readClipboardImageViaXclip(runner clipboardCommandRunner) *clipboardImage {
	targets := runner("xclip", []string{"-selection", "clipboard", "-t", "TARGETS", "-o"}, clipboardListTimeout)
	var tryTypes []string
	if targets.OK {
		if preferred := selectPreferredImageMimeType(splitClipboardMimeTypes(targets.Stdout)); preferred != "" {
			tryTypes = append(tryTypes, preferred)
		}
	}
	tryTypes = appendUniqueStrings(tryTypes, "image/png", "image/jpeg", "image/webp", "image/gif")
	for _, mimeType := range tryTypes {
		data := runner("xclip", []string{"-selection", "clipboard", "-t", mimeType, "-o"}, clipboardReadTimeout)
		if data.OK && len(data.Stdout) > 0 {
			return &clipboardImage{Bytes: data.Stdout, MimeType: baseMimeType(mimeType)}
		}
	}
	return nil
}

func readClipboardImageViaPowerShell(runner clipboardCommandRunner) *clipboardImage {
	tmp, err := os.CreateTemp("", "pi-wsl-clip-*.png")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	winPathResult := runner("wslpath", []string{"-w", tmpPath}, clipboardListTimeout)
	if !winPathResult.OK {
		return nil
	}
	winPath := strings.TrimSpace(string(winPathResult.Stdout))
	if winPath == "" {
		return nil
	}
	script := clipboardPowerShellScript(winPath)
	result := runner("powershell.exe", []string{"-NoProfile", "-Command", script}, clipboardPowerTimeout)
	if !result.OK || strings.TrimSpace(string(result.Stdout)) != "ok" {
		return nil
	}
	bytes, err := os.ReadFile(tmpPath)
	if err != nil || len(bytes) == 0 {
		return nil
	}
	return &clipboardImage{Bytes: bytes, MimeType: "image/png"}
}

func readClipboardImageViaWindowsPowerShell(runner clipboardCommandRunner) *clipboardImage {
	tmp, err := os.CreateTemp("", "pi-win-clip-*.png")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	script := clipboardPowerShellScript(tmpPath)
	result := runner("powershell.exe", []string{"-NoProfile", "-Command", script}, clipboardPowerTimeout)
	if !result.OK || strings.TrimSpace(string(result.Stdout)) != "ok" {
		return nil
	}
	bytes, err := os.ReadFile(tmpPath)
	if err != nil || len(bytes) == 0 {
		return nil
	}
	return &clipboardImage{Bytes: bytes, MimeType: "image/png"}
}

func clipboardPowerShellScript(path string) string {
	quoted := strings.ReplaceAll(path, "'", "''")
	return strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		"$path = '" + quoted + "'",
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }",
	}, "; ")
}

func runClipboardImageCommand(command string, args []string, timeout time.Duration) clipboardCommandResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return clipboardCommandResult{}
	}
	return clipboardCommandResult{Stdout: out, OK: true}
}

func writeClipboardImageTempFile(image clipboardImage) (string, error) {
	ext := extensionForImageMimeType(image.MimeType)
	if ext == "" {
		ext = "png"
	}
	file, err := os.CreateTemp("", "pi-clipboard-*."+ext)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(image.Bytes); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

func extensionForImageMimeType(mimeType string) string {
	switch baseMimeType(mimeType) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return ""
	}
}

func selectPreferredImageMimeType(mimeTypes []string) string {
	normalized := make(map[string]string, len(mimeTypes))
	for _, mimeType := range mimeTypes {
		raw := strings.TrimSpace(mimeType)
		if raw == "" {
			continue
		}
		normalized[baseMimeType(raw)] = raw
	}
	for _, preferred := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if raw, ok := normalized[preferred]; ok {
			return raw
		}
	}
	// Fall back to the first image/* type in input order. Ranging the map here
	// would pick a nondeterministic type when several non-preferred image types
	// are offered.
	for _, mimeType := range mimeTypes {
		raw := strings.TrimSpace(mimeType)
		if raw != "" && strings.HasPrefix(baseMimeType(raw), "image/") {
			return raw
		}
	}
	return ""
}

func baseMimeType(mimeType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
}

func splitClipboardMimeTypes(stdout []byte) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(stdout), "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if !seen[value] {
			values = append(values, value)
			seen[value] = true
		}
	}
	return values
}

func envSliceToMap(env []string) map[string]string {
	out := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func isWaylandEnv(env map[string]string) bool {
	return env["WAYLAND_DISPLAY"] != "" || env["XDG_SESSION_TYPE"] == "wayland"
}

func isWSLEnv(env map[string]string) bool {
	if env["WSL_DISTRO_NAME"] != "" || env["WSLENV"] != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(data)), "microsoft")
}
