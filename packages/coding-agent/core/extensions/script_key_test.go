package extensions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// TestScriptExtensionKeyVirtualExport loads a real script extension through the
// node bridge to verify the @earendil-works/pi-tui virtual module now exports
// Key, including its modifier helpers (Key.ctrlAlt('p') -> 'ctrl+alt+p'). This
// also locks the Go-raw-string escaping of the Key source.
func TestScriptExtensionKeyVirtualExport(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "key-ext.mjs")
	source := `
import { Key, matchesKey } from "@earendil-works/pi-tui";

export default function (pi) {
	pi.registerTool({
		name: "keyprobe",
		description: "report a key chord",
		parameters: { type: "object", properties: {} },
		execute() {
			const chord = Key.ctrlAlt("p");
			const ok = matchesKey("enter", Key.enter);
			return { content: [{ type: "text", text: chord + " " + String(ok) }] };
		},
	});
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
	var probe *ToolDefinition
	tools := runtime.API.SnapshotTools()
	for i := range tools {
		if tools[i].Name == "keyprobe" {
			probe = &tools[i]
			break
		}
	}
	if probe == nil {
		t.Fatalf("keyprobe tool not registered; tools=%v", tools)
	}
	result, err := probe.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
	if !strings.Contains(got, "ctrl+alt+p") || !strings.Contains(got, "true") {
		t.Fatalf("Key export not wired correctly, got %q", got)
	}
}

// TestScriptExtensionUnsupportedShortcutFailsFast verifies pi.registerShortcut
// raises a clear unsupported error rather than an opaque "not a function".
func TestScriptExtensionUnsupportedShortcutFailsFast(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "shortcut-ext.mjs")
	source := `
import { Key } from "@earendil-works/pi-tui";
export default function (pi) {
	pi.registerShortcut(Key.ctrlAlt("p"), { handler() {} });
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	api := NewAPI()
	errs := LoadScriptExtensions(context.Background(), api, []string{ext}, nil)
	if len(errs) == 0 {
		t.Fatal("expected an error from registerShortcut")
	}
	if !strings.Contains(errs[0].Error(), "registerShortcut is unsupported") {
		t.Fatalf("error should explain the unsupported API, got: %v", errs[0])
	}
}
