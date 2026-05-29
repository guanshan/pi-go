package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

const (
	defaultShareViewerURL     = "https://pi.dev/session/"
	maxOSC52EncodedClipboard  = 100_000
	clipboardCommandTimeout   = 5 * time.Second
	shareTemporaryFilePattern = "pi-session-*.html"
	shareViewerURLEnvironment = "PI_SHARE_VIEWER_URL"
)

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type ShareResult struct {
	PreviewURL string `json:"previewUrl"`
	GistURL    string `json:"gistUrl"`
	GistID     string `json:"gistId"`
	ExportPath string `json:"exportPath"`
}

var (
	externalCommand  = defaultExternalCommand
	externalLookPath = exec.LookPath
	envValue         = os.Getenv
	goosValue        = func() string { return runtime.GOOS }
)

func (a *AgentSession) GetLastAssistantText() string {
	if a == nil || a.Session == nil {
		return ""
	}
	messages := a.Session.BuildContext().Messages
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if ai.MessageRole(msg) != "assistant" {
			continue
		}
		assistant, _ := ai.AsAssistantMessage(msg)
		if assistant.StopReason == "aborted" && len(ai.MessageBlocks(msg)) == 0 {
			continue
		}
		var parts []string
		for _, block := range ai.MessageBlocks(msg) {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		text := strings.TrimSpace(strings.Join(parts, ""))
		if text != "" {
			return text
		}
	}
	return ""
}

func CopyToClipboard(text string) error {
	return CopyTextToClipboard(text, os.Stdout)
}

func CopyTextToClipboard(text string, osc52Writer io.Writer) error {
	copied := false
	platform := goosValue()
	switch platform {
	case "darwin":
		copied = runClipboardTool("pbcopy", nil, text) == nil
	case "windows":
		copied = runClipboardTool("clip", nil, text) == nil
	default:
		if envValue("TERMUX_VERSION") != "" {
			copied = runClipboardTool("termux-clipboard-set", nil, text) == nil
		}
		if !copied && envValue("WAYLAND_DISPLAY") != "" {
			copied = runClipboardTool("wl-copy", nil, text) == nil
		}
		if !copied && envValue("DISPLAY") != "" {
			if runClipboardTool("xclip", []string{"-selection", "clipboard"}, text) == nil {
				copied = true
			} else if runClipboardTool("xsel", []string{"--clipboard", "--input"}, text) == nil {
				copied = true
			}
		}
	}

	if isRemoteClipboardSession() || !copied {
		if emitOSC52Clipboard(text, osc52Writer) {
			copied = true
		}
	}
	if !copied {
		return errors.New("failed to copy to clipboard")
	}
	return nil
}

func runClipboardTool(name string, args []string, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clipboardCommandTimeout)
	defer cancel()
	result, err := externalCommand(ctx, name, args, text)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s exited with code %d", name, result.ExitCode)
	}
	return nil
}

func isRemoteClipboardSession() bool {
	return envValue("SSH_CONNECTION") != "" || envValue("SSH_CLIENT") != "" || envValue("MOSH_CONNECTION") != ""
}

func emitOSC52Clipboard(text string, w io.Writer) bool {
	if w == nil {
		return false
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > maxOSC52EncodedClipboard {
		return false
	}
	_, err := fmt.Fprintf(w, "\x1b]52;c;%s\a", encoded)
	return err == nil
}

func GetShareViewerURL(gistID string) string {
	baseURL := envValue(shareViewerURLEnvironment)
	if baseURL == "" {
		baseURL = defaultShareViewerURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	return baseURL + gistID
}

func ShareSessionHTML(ctx context.Context, sessionFile string) (ShareResult, error) {
	if sessionFile == "" {
		return ShareResult{}, errors.New("cannot share an in-memory session")
	}
	if _, err := externalLookPath("gh"); err != nil {
		return ShareResult{}, errors.New("GitHub CLI (gh) is not installed. Install it from https://cli.github.com/")
	}
	authResult, err := externalCommand(ctx, "gh", []string{"auth", "status"}, "")
	if err != nil {
		return ShareResult{}, err
	}
	if authResult.ExitCode != 0 {
		return ShareResult{}, errors.New("GitHub CLI is not logged in; run 'gh auth login' first")
	}

	tmp, err := os.CreateTemp("", shareTemporaryFilePattern)
	if err != nil {
		return ShareResult{}, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if _, err := ExportSessionToHTML(sessionFile, tmpPath); err != nil {
		return ShareResult{}, fmt.Errorf("failed to export session: %w", err)
	}
	gistResult, err := externalCommand(ctx, "gh", []string{"gist", "create", "--public=false", tmpPath}, "")
	if err != nil {
		return ShareResult{}, err
	}
	if gistResult.ExitCode != 0 {
		message := strings.TrimSpace(gistResult.Stderr)
		if message == "" {
			message = "unknown error"
		}
		return ShareResult{}, fmt.Errorf("failed to create gist: %s", message)
	}
	gistURL := strings.TrimSpace(gistResult.Stdout)
	gistID := parseGistID(gistURL)
	if gistID == "" {
		return ShareResult{}, errors.New("failed to parse gist ID from gh output")
	}
	return ShareResult{
		PreviewURL: GetShareViewerURL(gistID),
		GistURL:    gistURL,
		GistID:     gistID,
		ExportPath: filepath.Base(tmpPath),
	}, nil
}

func parseGistID(gistURL string) string {
	gistURL = strings.TrimRight(strings.TrimSpace(gistURL), "/")
	if gistURL == "" {
		return ""
	}
	idx := strings.LastIndex(gistURL, "/")
	if idx < 0 {
		return gistURL
	}
	return gistURL[idx+1:]
}

func defaultExternalCommand(ctx context.Context, name string, args []string, input string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	result.ExitCode = -1
	return result, err
}
