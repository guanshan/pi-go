package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScriptExtensionMessageRendererAndSendMessage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "message-renderer-ext.mjs")
	source := `
import { Box, Text } from "@earendil-works/pi-tui";

export default function (pi) {
	pi.registerMessageRenderer("status-update", (message, options, theme) => {
		const box = new Box();
		box.addChild(new Text(theme.bold("RENDER:" + message.content)));
		if (options.expanded) {
			box.addChild(new Text("level:" + (message.details?.level ?? "")));
		}
		return box;
	});
	pi.registerCommand("emit", {
		description: "emit a status update",
		handler(args) {
			return pi.sendMessage(
				{ customType: "status-update", content: args, display: false, details: { level: "warn" } },
				{ triggerTurn: true }
			).then(() => "sent");
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	actionCh := make(chan ExtensionContextAction, 1)
	runtime := NewRunnerWithAPI(NewAPI())
	runtime.SetContextActionHandler(func(_ context.Context, action ExtensionContextAction) (json.RawMessage, error) {
		actionCh <- action
		return json.RawMessage(`{"ok":true}`), nil
	})
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if renderers := runtime.RegisteredMessageRenderers(); len(renderers) != 1 || renderers[0].CustomType != "status-update" || renderers[0].Source != ext {
		t.Fatalf("message renderers=%#v", renderers)
	}

	rendered, handled, err := runtime.RenderMessage(context.Background(), MessageRenderRequest{
		CustomType: "status-update",
		Content:    "hello",
		Display:    true,
		Details:    map[string]any{"level": "warn"},
		Expanded:   true,
		Width:      80,
	})
	if err != nil || !handled {
		t.Fatalf("render handled=%v err=%v", handled, err)
	}
	// theme.bold now emits real ANSI SGR (the renderer-fidelity slice); the
	// Text("level:warn") child is unstyled.
	if got, want := rendered.Lines, []string{"\x1b[1mRENDER:hello\x1b[22m", "level:warn"}; !equalStrings(got, want) {
		t.Fatalf("rendered lines=%#v want %#v", got, want)
	}

	out, handled, err := runtime.ExecuteCommand(context.Background(), "emit", "from-command")
	if err != nil || !handled || out != "sent" {
		t.Fatalf("execute command handled=%v out=%q err=%v", handled, out, err)
	}
	select {
	case action := <-actionCh:
		if action.Name != "sendMessage" {
			t.Fatalf("action name=%q", action.Name)
		}
		var payload struct {
			Message struct {
				CustomType string         `json:"customType"`
				Content    string         `json:"content"`
				Display    bool           `json:"display"`
				Details    map[string]any `json:"details"`
			} `json:"message"`
			Options struct {
				TriggerTurn bool `json:"triggerTurn"`
			} `json:"options"`
		}
		if err := json.Unmarshal(action.Params, &payload); err != nil {
			t.Fatalf("decode action params: %v (%s)", err, action.Params)
		}
		if payload.Message.CustomType != "status-update" || payload.Message.Content != "from-command" || payload.Message.Display {
			t.Fatalf("message payload=%#v", payload.Message)
		}
		if payload.Message.Details["level"] != "warn" || !payload.Options.TriggerTurn {
			t.Fatalf("message options/details=%#v %#v", payload.Options, payload.Message.Details)
		}
	case <-time.After(time.Second):
		t.Fatal("sendMessage action was not emitted")
	}
}

// TestScriptExtensionMessageRendererANSIAndFallback verifies the renderer
// fidelity slice: theme fg/bg/bold emit ANSI SGR, Box applies its style function
// per line, and a renderer returning undefined is handled with empty Lines (so the
// Go host falls back to default rendering).
func TestScriptExtensionMessageRendererANSIAndFallback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "ansi-renderer-ext.mjs")
	source := `
import { Box, Text } from "@earendil-works/pi-tui";

export default function (pi) {
	pi.registerMessageRenderer("styled", (message, options, theme) => {
		const box = new Box(0, 0, (line) => theme.bg("info", line));
		box.addChild(new Text(theme.fg("error", "E") + theme.underline("U")));
		return box;
	});
	pi.registerMessageRenderer("blank", () => undefined);
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := NewRunnerWithAPI(NewAPI())
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}

	styled, handled, err := runtime.RenderMessage(context.Background(), MessageRenderRequest{CustomType: "styled", Content: "x", Display: true, Width: 80})
	if err != nil || !handled || len(styled.Lines) != 1 {
		t.Fatalf("styled render handled=%v err=%v lines=%#v", handled, err, styled.Lines)
	}
	line := styled.Lines[0]
	// fg(error) -> 31, underline -> 4, and the Box bg(info) -> 44 wraps the line.
	for _, want := range []string{"\x1b[44m", "\x1b[31mE\x1b[39m", "\x1b[4mU\x1b[24m"} {
		if !strings.Contains(line, want) {
			t.Fatalf("rendered line %q missing ANSI %q", line, want)
		}
	}

	blank, handled, err := runtime.RenderMessage(context.Background(), MessageRenderRequest{CustomType: "blank", Content: "x", Display: true, Width: 80})
	if err != nil || !handled {
		t.Fatalf("blank render handled=%v err=%v", handled, err)
	}
	if len(blank.Lines) != 0 {
		t.Fatalf("undefined renderer should yield empty Lines, got %#v", blank.Lines)
	}
}

func TestScriptExtensionSessionActionAPI(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "session-actions-ext.mjs")
	source := `
import { getSettingsListTheme } from "@earendil-works/pi-coding-agent";

export default function (pi) {
	pi.registerCommand("actions", {
		description: "exercise session actions",
		async handler() {
			const theme = getSettingsListTheme();
			await pi.sendUserMessage([
				{ type: "text", text: "hello" },
				{ type: "image", mimeType: "image/png", data: "abc" },
			], { deliverAs: "followUp" });
			await pi.appendEntry("state", { value: 1 });
			await pi.setSessionName("Demo");
			const name = await pi.getSessionName();
			await pi.setLabel("entry-1", "important");
			return theme.label("name:" + name, true);
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	actionCh := make(chan ExtensionContextAction, 5)
	runtime := NewRunnerWithAPI(NewAPI())
	runtime.SetContextActionHandler(func(_ context.Context, action ExtensionContextAction) (json.RawMessage, error) {
		actionCh <- action
		if action.Name == "getSessionName" {
			return json.RawMessage(`"Demo"`), nil
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	out, handled, err := runtime.ExecuteCommand(context.Background(), "actions", "")
	if err != nil || !handled || out != "name:Demo" {
		t.Fatalf("execute command handled=%v out=%q err=%v", handled, out, err)
	}
	actions := make([]ExtensionContextAction, 0, 5)
	for len(actions) < 5 {
		select {
		case action := <-actionCh:
			actions = append(actions, action)
		case <-time.After(time.Second):
			t.Fatalf("actions=%#v", actions)
		}
	}
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, action.Name)
	}
	if got, want := names, []string{"sendUserMessage", "appendEntry", "setSessionName", "getSessionName", "setLabel"}; !equalStrings(got, want) {
		t.Fatalf("actions=%#v want %#v", got, want)
	}
	var userPayload struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Data     string `json:"data"`
		} `json:"content"`
		Options struct {
			DeliverAs string `json:"deliverAs"`
		} `json:"options"`
	}
	if err := json.Unmarshal(actions[0].Params, &userPayload); err != nil {
		t.Fatalf("decode user payload: %v (%s)", err, actions[0].Params)
	}
	if len(userPayload.Content) != 2 || userPayload.Content[0].Text != "hello" || userPayload.Content[1].MimeType != "image/png" || userPayload.Options.DeliverAs != "followUp" {
		t.Fatalf("user payload=%#v", userPayload)
	}
	var entryPayload struct {
		CustomType string         `json:"customType"`
		Data       map[string]any `json:"data"`
	}
	if err := json.Unmarshal(actions[1].Params, &entryPayload); err != nil {
		t.Fatalf("decode append payload: %v (%s)", err, actions[1].Params)
	}
	if entryPayload.CustomType != "state" || entryPayload.Data["value"] != float64(1) {
		t.Fatalf("append payload=%#v", entryPayload)
	}
	var labelPayload struct {
		EntryID string `json:"entryId"`
		Label   string `json:"label"`
	}
	if err := json.Unmarshal(actions[4].Params, &labelPayload); err != nil {
		t.Fatalf("decode label payload: %v (%s)", err, actions[4].Params)
	}
	if labelPayload.EntryID != "entry-1" || labelPayload.Label != "important" {
		t.Fatalf("label payload=%#v", labelPayload)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
