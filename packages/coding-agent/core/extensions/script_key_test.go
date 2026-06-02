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

// TestScriptExtensionUnsupportedRegisterDegradesGracefully verifies that the
// unsupported registration APIs (registerShortcut/registerProvider/
// registerMessageRenderer/addAutocompleteProvider) warn and skip instead of
// throwing at load time (4.md), so the rest of the extension still loads.
func TestScriptExtensionUnsupportedRegisterDegradesGracefully(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "shortcut-ext.mjs")
	// The extension calls several unsupported register* APIs AND registers a
	// tool. Graceful degradation means the unsupported calls are skipped (not
	// fatal) and the tool is still registered.
	source := `
import { Key } from "@earendil-works/pi-tui";
export default function (pi) {
	pi.registerShortcut(Key.ctrlAlt("p"), { handler() {} });
	pi.registerProvider({ name: "custom" });
	pi.registerMessageRenderer("custom", () => []);
	pi.addAutocompleteProvider(() => []);
	pi.registerTool({
		name: "survivor",
		description: "still registered despite unsupported register* calls",
		parameters: { type: "object", properties: {} },
		execute() { return { content: [{ type: "text", text: "ok" }] }; },
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := NewRunnerWithAPI(NewAPI())
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil)
	if len(errs) != 0 {
		t.Fatalf("expected graceful load (no errors) from unsupported register* calls, got: %v", errs)
	}
	// The tool registered after the unsupported calls must survive.
	found := false
	for _, tool := range runtime.API.SnapshotTools() {
		if tool.Name == "survivor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool registered after unsupported register* calls was lost; tools=%v", runtime.API.SnapshotTools())
	}
}
