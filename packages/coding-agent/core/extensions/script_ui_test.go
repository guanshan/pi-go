package extensions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptExtensionUIConfirmReturnsExplicitError verifies that ctx.ui.confirm
// rejects with an explicit error in the headless Go bridge instead of silently
// returning false (1.md P1-2 / safety). A confirm-gated extension must not
// quietly take the wrong branch.
func TestScriptExtensionUIConfirmReturnsExplicitError(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "confirm-ext.mjs")
	source := `
export default function (pi) {
	pi.registerTool({
		name: "ask",
		description: "asks for confirmation",
		parameters: { type: "object", properties: {} },
		async execute(_id, _params, _a, _b, ctx) {
			const ok = await ctx.ui.confirm("Run destructive command?");
			return { content: [{ type: "text", text: "confirmed=" + String(ok) }] };
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
	var ask *ToolDefinition
	tools := runtime.API.SnapshotTools()
	for i := range tools {
		if tools[i].Name == "ask" {
			ask = &tools[i]
			break
		}
	}
	if ask == nil {
		t.Fatalf("ask tool not registered; tools=%v", tools)
	}
	_, err := ask.Execute(context.Background(), []byte("{}"))
	if err == nil {
		t.Fatal("expected ctx.ui.confirm to surface an error in headless mode, got nil (silent answer)")
	}
	if !strings.Contains(err.Error(), "ui.confirm") || !strings.Contains(err.Error(), "interactive input") {
		t.Fatalf("error should explain the headless ui.confirm limitation, got: %v", err)
	}
}

// TestScriptExtensionUISelectReturnsExplicitError verifies that ctx.ui.select
// rejects with an explicit error in the headless Go bridge instead of silently
// returning the first option.
func TestScriptExtensionUISelectReturnsExplicitError(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "select-ext.mjs")
	source := `
export default function (pi) {
	pi.registerTool({
		name: "pick",
		description: "picks an option",
		parameters: { type: "object", properties: {} },
		async execute(_id, _params, _a, _b, ctx) {
			const choice = await ctx.ui.select("Pick", ["a", "b", "c"]);
			return { content: [{ type: "text", text: "choice=" + String(choice) }] };
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
	var pick *ToolDefinition
	tools := runtime.API.SnapshotTools()
	for i := range tools {
		if tools[i].Name == "pick" {
			pick = &tools[i]
			break
		}
	}
	if pick == nil {
		t.Fatalf("pick tool not registered; tools=%v", tools)
	}
	_, err := pick.Execute(context.Background(), []byte("{}"))
	if err == nil {
		t.Fatal("expected ctx.ui.select to surface an error in headless mode, got nil (silent first-option)")
	}
	if !strings.Contains(err.Error(), "ui.select") || !strings.Contains(err.Error(), "interactive input") {
		t.Fatalf("error should explain the headless ui.select limitation, got: %v", err)
	}
}
