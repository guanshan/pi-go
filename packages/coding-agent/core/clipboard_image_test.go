package core

import (
	"encoding/base64"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestReadClipboardImageViaWlPastePrefersSupportedMime(t *testing.T) {
	calls := []string{}
	runner := func(command string, args []string, timeout time.Duration) clipboardCommandResult {
		calls = append(calls, command+" "+strings.Join(args, " "))
		switch {
		case command == "wl-paste" && strings.Join(args, " ") == "--list-types":
			return clipboardCommandResult{OK: true, Stdout: []byte("text/plain\nimage/jpeg\nimage/png\n")}
		case command == "wl-paste" && strings.Join(args, " ") == "--type image/png --no-newline":
			return clipboardCommandResult{OK: true, Stdout: []byte("png-bytes")}
		default:
			return clipboardCommandResult{}
		}
	}
	image, err := readClipboardImageWithEnv("linux", []string{"WAYLAND_DISPLAY=wayland-1"}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if image == nil || image.MimeType != "image/png" || string(image.Bytes) != "png-bytes" {
		t.Fatalf("image=%#v", image)
	}
	if len(calls) != 2 || calls[1] != "wl-paste --type image/png --no-newline" {
		t.Fatalf("calls=%#v", calls)
	}
}

func TestInteractiveClipboardImagePasteAttachesImageBlock(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("clipboard image paste handler test assumes linux platform selection")
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	oldRunner := defaultClipboardCommandRunner
	defer func() { defaultClipboardCommandRunner = oldRunner }()
	defaultClipboardCommandRunner = func(command string, args []string, timeout time.Duration) clipboardCommandResult {
		switch {
		case command == "wl-paste" && strings.Join(args, " ") == "--list-types":
			return clipboardCommandResult{OK: true, Stdout: []byte("image/png\n")}
		case command == "wl-paste" && strings.Join(args, " ") == "--type image/png --no-newline":
			return clipboardCommandResult{OK: true, Stdout: []byte{0x89, 'P', 'N', 'G'}}
		default:
			return clipboardCommandResult{}
		}
	}

	runtimeSession := testInteractiveRuntime(t)
	model, err := newInteractiveModel(t.Context(), runtimeSession, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model.handleClipboardImagePaste()
	if len(model.inputImages) != 1 {
		t.Fatalf("inputImages=%#v", model.inputImages)
	}
	if model.inputImages[0].MimeType != "image/png" || model.inputImages[0].Data != base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("image block=%#v", model.inputImages[0])
	}
	path := strings.TrimSpace(model.input.Value())
	if !strings.Contains(path, "pi-clipboard-") || !strings.HasSuffix(path, ".png") {
		t.Fatalf("input path=%q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("temp image not written: %v", err)
	}
	defer os.Remove(path)

	cmd := model.submitInputWithBehavior("")
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}
	if done, ok := cmd().(interactivePromptDoneMsg); !ok || done.Err != nil {
		t.Fatalf("prompt result=%#v", done)
	}
	ctx := runtimeSession.Session().Session.BuildContext()
	if len(ctx.Messages) == 0 {
		t.Fatal("no user message recorded")
	}
	blocks := ai.MessageBlocks(ctx.Messages[0])
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].MimeType != "image/png" {
		t.Fatalf("user blocks=%#v", blocks)
	}
	if len(model.inputImages) != 0 {
		t.Fatalf("inputImages not cleared: %#v", model.inputImages)
	}
}

func TestClipboardImageUnsupportedPlatformReportsError(t *testing.T) {
	image, err := readClipboardImageWithEnv("plan9", nil, nil)
	if err == nil || image != nil || !strings.Contains(err.Error(), "unsupported on plan9") {
		t.Fatalf("image=%#v err=%v", image, err)
	}
}
