package extensions

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestScriptExtensionUISetStatusSendsRequest(t *testing.T) {
	api := NewAPI()
	seen := make(chan json.RawMessage, 1)
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "setStatus" {
			t.Errorf("method=%q want setStatus", method)
		}
		seen <- append(json.RawMessage(nil), params...)
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "status",
		`ctx.ui.setStatus("build", "Building");
		await new Promise((resolve) => setTimeout(resolve, 20));
		return { content: [{ type: "text", text: "ok" }] };`)
	if _, err := tool.Execute(context.Background(), []byte("{}")); err != nil {
		t.Fatalf("execute: %v", err)
	}
	select {
	case raw := <-seen:
		var got struct {
			Key  string `json:"key"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Key != "build" || got.Text != "Building" {
			t.Fatalf("status payload=%#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for setStatus request")
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
	deadline := time.Now().Add(time.Second)
	for {
		res, err = tool.Execute(context.Background(), []byte("{}"))
		if err != nil {
			t.Fatalf("execute (post-bind): %v", err)
		}
		if got := toolResultText(res); got == "hasUI=true" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("post-bind result=%q want hasUI=true (set_has_ui not delivered)", toolResultText(res))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSendSetHasUIIgnoresStaleSeq verifies the ordering guard: an out-of-order
// dispatch goroutine carrying an older sequence must not overwrite a newer
// hasUI state, so a rapid bind/unbind resolves to its latest value.
func TestSendSetHasUIIgnoresStaleSeq(t *testing.T) {
	r := &scriptRuntime{hasUIWake: make(chan struct{}, 1)}

	r.sendSetHasUI(2, false)
	r.sendSetHasUI(1, true) // stale: lower seq than what's already recorded

	r.hasUIMu.Lock()
	pending, seq := r.hasUIPending, r.hasUISeq
	r.hasUIMu.Unlock()
	if pending != false || seq != 2 {
		t.Fatalf("stale seq applied: pending=%v seq=%d, want false/2", pending, seq)
	}

	r.sendSetHasUI(3, true) // newer: applies
	r.hasUIMu.Lock()
	pending = r.hasUIPending
	r.hasUIMu.Unlock()
	if pending != true {
		t.Fatalf("newer seq not applied: pending=%v, want true", pending)
	}
}

func TestSetUIHandlerDoesNotBlockOnSlowListener(t *testing.T) {
	api := NewAPI()
	started := make(chan struct{})
	release := make(chan struct{})
	api.registerUIListener(func(uint64, bool) {
		close(started)
		<-release
	})

	done := make(chan struct{})
	go func() {
		api.SetUIHandler(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage("null"), nil
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatal("SetUIHandler blocked on a listener")
	}
	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		close(release)
		t.Fatal("listener was not invoked")
	}
	close(release)
}

// TestScriptExtensionUILightweightSettersSendRequests verifies the fire-and-forget
// lightweight ctx.ui.* setters (Item 1) each emit a ui_request to the bound host
// handler with the expected params. One extension calls all seven; the requests
// and the tool result share the node->host stream in order, so by the time
// Execute returns the handler has seen all of them.
func TestScriptExtensionUILightweightSettersSendRequests(t *testing.T) {
	api := NewAPI()
	var mu sync.Mutex
	got := map[string]json.RawMessage{}
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		got[method] = append(json.RawMessage(nil), params...)
		mu.Unlock()
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "setters",
		`ctx.ui.setWorkingMessage("Crunching");
		ctx.ui.setWorkingVisible(false);
		ctx.ui.setWorkingIndicator({ frames: ["A", "B"], intervalMs: 120 });
		ctx.ui.setHiddenThinkingLabel("hidden");
		ctx.ui.setTitle("My Title");
		ctx.ui.pasteToEditor("pasted text");
		ctx.ui.setEditorText("replaced text");
		await new Promise((r) => setTimeout(r, 60));
		return { content: [{ type: "text", text: "ok" }] };`)
	if _, err := tool.Execute(context.Background(), []byte("{}")); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Poll briefly in case the last request is still in flight after Execute.
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 7 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	field := func(method, key string) any {
		raw, ok := got[method]
		if !ok {
			t.Fatalf("no %s request captured; got=%v", method, keys(got))
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal %s params: %v", method, err)
		}
		return m[key]
	}
	if v := field("setWorkingMessage", "message"); v != "Crunching" {
		t.Errorf("setWorkingMessage message=%v", v)
	}
	if v := field("setWorkingVisible", "visible"); v != false {
		t.Errorf("setWorkingVisible visible=%v", v)
	}
	if v := field("setWorkingIndicator", "intervalMs"); v != float64(120) {
		t.Errorf("setWorkingIndicator intervalMs=%v", v)
	}
	if frames, ok := field("setWorkingIndicator", "frames").([]any); !ok || len(frames) != 2 || frames[0] != "A" {
		t.Errorf("setWorkingIndicator frames=%v", field("setWorkingIndicator", "frames"))
	}
	if v := field("setHiddenThinkingLabel", "label"); v != "hidden" {
		t.Errorf("setHiddenThinkingLabel label=%v", v)
	}
	if v := field("setTitle", "title"); v != "My Title" {
		t.Errorf("setTitle title=%v", v)
	}
	if v := field("pasteToEditor", "text"); v != "pasted text" {
		t.Errorf("pasteToEditor text=%v", v)
	}
	if v := field("setEditorText", "text"); v != "replaced text" {
		t.Errorf("setEditorText text=%v", v)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestScriptExtensionUISetWidget verifies ctx.ui.setWidget forwards string-array
// (and undefined-to-remove) widgets to the host, while the unsupported component
// setters (setFooter/setHeader/setEditorComponent) warn-and-no-op (no host
// request) and getEditorComponent resolves undefined.
func TestScriptExtensionUISetWidget(t *testing.T) {
	api := NewAPI()
	var mu sync.Mutex
	var reqs []struct {
		method string
		params json.RawMessage
	}
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		reqs = append(reqs, struct {
			method string
			params json.RawMessage
		}{method, append(json.RawMessage(nil), params...)})
		mu.Unlock()
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "widget",
		`ctx.ui.setWidget("status", ["line A", "line B"], { placement: "belowEditor" });
		ctx.ui.setWidget("gone", undefined);
		ctx.ui.setFooter(() => ({}));
		ctx.ui.setHeader(() => ({}));
		ctx.ui.setEditorComponent(() => ({}));
		const ec = ctx.ui.getEditorComponent();
		await new Promise((r) => setTimeout(r, 60));
		return { content: [{ type: "text", text: "ec=" + String(ec) }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "ec=undefined" {
		t.Fatalf("getEditorComponent result=%q want ec=undefined", got)
	}
	mu.Lock()
	defer mu.Unlock()
	// Only the two setWidget calls reach the host; setFooter/setHeader/
	// setEditorComponent are client-side no-ops.
	var widgets []json.RawMessage
	for _, r := range reqs {
		if r.method != "setWidget" {
			t.Fatalf("unexpected host request method %q (component setters must no-op)", r.method)
		}
		widgets = append(widgets, r.params)
	}
	if len(widgets) != 2 {
		t.Fatalf("got %d setWidget requests, want 2", len(widgets))
	}
	// The host dispatches ui_request handlers concurrently, so the two setWidget
	// calls may be captured in either order; index by key.
	byKey := map[string]struct {
		Key       string    `json:"key"`
		Lines     *[]string `json:"lines"`
		Placement string    `json:"placement"`
	}{}
	for _, raw := range widgets {
		var w struct {
			Key       string    `json:"key"`
			Lines     *[]string `json:"lines"`
			Placement string    `json:"placement"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
		byKey[w.Key] = w
	}
	status, ok := byKey["status"]
	if !ok || status.Lines == nil || len(*status.Lines) != 2 || (*status.Lines)[0] != "line A" || status.Placement != "belowEditor" {
		t.Fatalf("status setWidget payload=%#v", status)
	}
	gone, ok := byKey["gone"]
	if !ok || gone.Lines != nil {
		t.Fatalf("remove setWidget payload=%#v (lines should be null)", gone)
	}
}

// TestScriptExtensionUIGetEditorTextReturnsHostValue verifies ctx.ui.getEditorText
// resolves the string the host returns (async Promise in the Go bridge).
func TestScriptExtensionUIGetEditorTextReturnsHostValue(t *testing.T) {
	api := NewAPI()
	api.SetUIHandler(func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		if method == "getEditorText" {
			return json.RawMessage(`"current draft"`), nil
		}
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "geteditor",
		`const text = await ctx.ui.getEditorText();
		return { content: [{ type: "text", text: "editor=" + text }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "editor=current draft" {
		t.Fatalf("result=%q want editor=current draft", got)
	}
}

// TestScriptExtensionUIEditorReturnsHostValue verifies ctx.ui.editor forwards the
// title/prefill and resolves the host's edited text.
func TestScriptExtensionUIEditorReturnsHostValue(t *testing.T) {
	api := NewAPI()
	var capturedTitle, capturedPrefill string
	api.SetUIHandler(func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method == "editor" {
			var p struct {
				Title   string `json:"title"`
				Prefill string `json:"prefill"`
			}
			_ = json.Unmarshal(params, &p)
			capturedTitle, capturedPrefill = p.Title, p.Prefill
			return json.RawMessage(`"edited body"`), nil
		}
		return json.RawMessage("null"), nil
	})
	tool := uiToolExtension(t, api, "editor",
		`const text = await ctx.ui.editor("Compose", "start here");
		return { content: [{ type: "text", text: "editor=" + String(text) }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "editor=edited body" {
		t.Fatalf("result=%q want editor=edited body", got)
	}
	if capturedTitle != "Compose" || capturedPrefill != "start here" {
		t.Fatalf("editor params title=%q prefill=%q", capturedTitle, capturedPrefill)
	}
}

// TestScriptExtensionUILightweightHeadlessNoOp verifies that with no host UI the
// lightweight APIs are callable without "undefined is not a function": the
// fire-and-forget setters no-op, getEditorText resolves "", editor resolves
// undefined, and onTerminalInput returns a no-op unsubscribe function.
func TestScriptExtensionUILightweightHeadlessNoOp(t *testing.T) {
	api := NewAPI() // no UI handler bound -> headless
	tool := uiToolExtension(t, api, "headless",
		`ctx.ui.setWorkingMessage("x");
		ctx.ui.setWorkingVisible(true);
		ctx.ui.setWorkingIndicator({ frames: [] });
		ctx.ui.setHiddenThinkingLabel("y");
		ctx.ui.setTitle("z");
		ctx.ui.pasteToEditor("p");
		ctx.ui.setEditorText("e");
		const text = await ctx.ui.getEditorText();
		const edited = await ctx.ui.editor("t", "p");
		const unsub = ctx.ui.onTerminalInput(() => ({ consume: true }));
		const unsubType = typeof unsub;
		unsub();
		return { content: [{ type: "text", text: "text=[" + text + "],edited=" + String(edited) + ",unsub=" + unsubType }] };`)
	res, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := toolResultText(res); got != "text=[],edited=undefined,unsub=function" {
		t.Fatalf("result=%q want text=[],edited=undefined,unsub=function", got)
	}
}
