package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// toolResultText returns the concatenated text of a tool result's content blocks.
func toolResultText(res ai.ToolResult) string {
	var b strings.Builder
	for _, block := range res.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

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
	if !strings.Contains(err.Error(), "ui.confirm") || !strings.Contains(err.Error(), "interactive host") {
		t.Fatalf("error should explain the missing UI handler, got: %v", err)
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
	if !strings.Contains(err.Error(), "ui.select") || !strings.Contains(err.Error(), "interactive host") {
		t.Fatalf("error should explain the missing UI handler, got: %v", err)
	}
}

// uiToolExtension writes a temp extension whose single tool calls one ctx.ui
// method and returns the result as text, then loads it and returns the tool.
func uiToolExtension(t *testing.T, api *API, toolName, body string) *ToolDefinition {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, toolName+"-ext.mjs")
	source := "export default function (pi) {\n" +
		"  pi.registerTool({ name: \"" + toolName + "\", description: \"x\", parameters: { type: \"object\", properties: {} },\n" +
		"    async execute(_id, _params, _a, _b, ctx) {\n" + body + "\n} });\n}\n"
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := NewRunnerWithAPI(api)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	tools := runtime.API.SnapshotTools()
	for i := range tools {
		if tools[i].Name == toolName {
			return &tools[i]
		}
	}
	t.Fatalf("%s tool not registered; tools=%v", toolName, tools)
	return nil
}

// TestScriptExtensionUIConfirmAnsweredByHandler verifies a bound UI handler
// answers ctx.ui.confirm over the reverse ui_request seam.
func TestScriptExtensionUIConfirmAnsweredByHandler(t *testing.T) {
	api := NewAPI()
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "confirm" {
			t.Errorf("method=%q want confirm", method)
		}
		var got struct{ Message, Detail string }
		_ = json.Unmarshal(params, &got)
		if got.Message != "Run destructive command?" {
			t.Errorf("confirm message=%q", got.Message)
		}
		return json.RawMessage("true"), nil
	})
	ask := uiToolExtension(t, api, "ask",
		`const ok = await ctx.ui.confirm("Run destructive command?");
		return { content: [{ type: "text", text: "confirmed=" + String(ok) }] };`)
	res, err := ask.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "confirmed=true" {
		t.Fatalf("result=%q want confirmed=true", got)
	}
}

// TestScriptExtensionUISelectAnsweredByHandler verifies a bound handler answers
// ctx.ui.select with the chosen value.
func TestScriptExtensionUISelectAnsweredByHandler(t *testing.T) {
	api := NewAPI()
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		var got struct {
			Message string
			Choices []string
		}
		_ = json.Unmarshal(params, &got)
		if len(got.Choices) != 3 || got.Choices[1] != "b" {
			t.Errorf("choices=%v", got.Choices)
		}
		return json.RawMessage(`"b"`), nil
	})
	pick := uiToolExtension(t, api, "pick",
		`const choice = await ctx.ui.select("Pick", ["a", "b", "c"]);
		return { content: [{ type: "text", text: "choice=" + String(choice) }] };`)
	res, err := pick.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "choice=b" {
		t.Fatalf("result=%q want choice=b", got)
	}
}

// TestScriptExtensionHasUIReflectsBoundHandler verifies ctx.hasUI tracks whether
// a UI handler is bound (TS model): true when bound before load.
func TestScriptExtensionHasUIReflectsBoundHandler(t *testing.T) {
	api := NewAPI()
	api.SetUIHandler(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "cap",
		`return { content: [{ type: "text", text: "hasUI=" + String(ctx.hasUI) }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "hasUI=true" {
		t.Fatalf("result=%q want hasUI=true", got)
	}
}

// TestScriptExtensionHasUIUpdatesOnLateBinding verifies ctx.hasUI starts false
// (no handler at load) and flips to true after SetUIHandler binds a handler
// post-load (the late-binding path the seam is built for), via set_has_ui.
func TestScriptExtensionHasUIUpdatesOnLateBinding(t *testing.T) {
	api := NewAPI()
	tool := uiToolExtension(t, api, "cap",
		`return { content: [{ type: "text", text: "hasUI=" + String(ctx.hasUI) }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute (pre-bind): %v", err)
	}
	if got := toolResultText(res); got != "hasUI=false" {
		t.Fatalf("pre-bind result=%q want hasUI=false", got)
	}
	api.SetUIHandler(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage("null"), nil
	})
	res, err = tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute (post-bind): %v", err)
	}
	if got := toolResultText(res); got != "hasUI=true" {
		t.Fatalf("post-bind result=%q want hasUI=true (set_has_ui not delivered)", got)
	}
}
