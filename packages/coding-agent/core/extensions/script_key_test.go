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

// TestScriptExtensionRegisterDegradesGracefully verifies that unsupported
// registration APIs and malformed provider/autocomplete definitions warn and
// skip instead of throwing at load time (4.md), while supported shortcut
// registration still survives.
func TestScriptExtensionUnsupportedRegisterDegradesGracefully(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "shortcut-ext.mjs")
	// The extension calls a supported shortcut registration, an incomplete
	// provider definition, a valid message renderer, a malformed autocomplete
	// provider, AND registers a tool. Graceful degradation means skipped calls
	// are not fatal and supported registrations still survive.
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
	if shortcuts := runtime.RegisteredShortcuts(); len(shortcuts) != 1 || shortcuts[0].Key != "ctrl+alt+p" {
		t.Fatalf("shortcut registration not bridged: %#v", shortcuts)
	}
	if providers := runtime.RegisteredProviders(); len(providers) != 0 {
		t.Fatalf("malformed provider should not register: %#v", providers)
	}
	if renderers := runtime.RegisteredMessageRenderers(); len(renderers) != 1 || renderers[0].CustomType != "custom" {
		t.Fatalf("message renderer registration not bridged: %#v", renderers)
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

func TestScriptExtensionShortcutExecutes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "shortcut-run-ext.mjs")
	source := `
import { Key } from "@earendil-works/pi-tui";

export default function (pi) {
	let count = 0;
	let cwd = "";
	pi.registerShortcut(Key.ctrlAlt("p"), {
		description: "Probe shortcut",
		handler(ctx) {
			count++;
			cwd = ctx.cwd;
		},
	});
	pi.registerTool({
		name: "shortcutprobe",
		description: "report shortcut execution",
		parameters: { type: "object", properties: {} },
		execute() {
			return { content: [{ type: "text", text: count + ":" + cwd }] };
		},
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := NewRunnerWithAPI(NewAPI())
	runtime.SetContextProvider(func() ExtensionContextSnapshot {
		return ExtensionContextSnapshot{CWD: dir, Mode: "tui", HasUI: true, IsIdle: true}
	})
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if shortcuts := runtime.RegisteredShortcuts(); len(shortcuts) != 1 || shortcuts[0].Description != "Probe shortcut" {
		t.Fatalf("shortcuts=%#v", shortcuts)
	}
	handled, err := runtime.ExecuteShortcut(context.Background(), "ctrl+alt+p")
	if err != nil || !handled {
		t.Fatalf("execute shortcut handled=%v err=%v", handled, err)
	}
	probe, ok := runtime.ToolDefinition("shortcutprobe")
	if !ok {
		t.Fatal("shortcutprobe tool missing")
	}
	result, err := probe.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute probe: %v", err)
	}
	got := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
	if got != "1:"+dir {
		t.Fatalf("shortcut handler state=%q", got)
	}
}

func TestScriptExtensionShortcutDynamicUpdates(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "shortcut-dynamic-ext.mjs")
	source := `
import { Key } from "@earendil-works/pi-tui";

export default function (pi) {
	let count = 0;
	pi.registerShortcut(Key.ctrlAlt("p"), {
		description: "Initial shortcut",
		handler() { count += 1; },
	});
	pi.registerCommand("swap-shortcut", {
		description: "swap the registered shortcut",
		handler() {
			pi.unregisterShortcut(Key.ctrlAlt("p"));
			pi.registerShortcut(Key.ctrlAlt("n"), {
				description: "Dynamic shortcut",
				handler() { count += 10; },
			});
			return "swapped";
		},
	});
	pi.registerTool({
		name: "shortcutcount",
		description: "report shortcut count",
		parameters: { type: "object", properties: {} },
		execute() {
			return { content: [{ type: "text", text: String(count) }] };
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
	if shortcuts := runtime.RegisteredShortcuts(); len(shortcuts) != 1 || shortcuts[0].Key != "ctrl+alt+p" {
		t.Fatalf("initial shortcuts=%#v", shortcuts)
	}
	out, handled, err := runtime.ExecuteCommand(context.Background(), "swap-shortcut", "")
	if err != nil || !handled || strings.TrimSpace(out) != "swapped" {
		t.Fatalf("swap command handled=%v out=%q err=%v", handled, out, err)
	}
	shortcuts := runtime.RegisteredShortcuts()
	if len(shortcuts) != 1 || shortcuts[0].Key != "ctrl+alt+n" || shortcuts[0].Description != "Dynamic shortcut" {
		t.Fatalf("dynamic shortcuts=%#v", shortcuts)
	}
	if handled, err := runtime.ExecuteShortcut(context.Background(), "ctrl+alt+p"); handled || err != nil {
		t.Fatalf("old shortcut should be unregistered, handled=%v err=%v", handled, err)
	}
	if handled, err := runtime.ExecuteShortcut(context.Background(), "ctrl+alt+n"); !handled || err != nil {
		t.Fatalf("new shortcut handled=%v err=%v", handled, err)
	}
	probe, ok := runtime.ToolDefinition("shortcutcount")
	if !ok {
		t.Fatal("shortcutcount tool missing")
	}
	result, err := probe.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute probe: %v", err)
	}
	got := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
	if got != "10" {
		t.Fatalf("shortcut count=%q", got)
	}
}

func TestScriptExtensionAutocompleteProvider(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "autocomplete-ext.mjs")
	source := `
export default function (pi) {
	pi.addAutocompleteProvider((current) => ({
		async getSuggestions(lines, cursorLine, cursorCol, options) {
			const text = String(lines[cursorLine] ?? "").slice(0, cursorCol);
			const match = text.match(/#(\d*)$/);
			if (!match) return current.getSuggestions(lines, cursorLine, cursorCol, options);
			return {
				prefix: "#" + match[1],
				items: [{ value: "#123", label: "#123", description: "Fix login flow" }],
			};
		},
		applyCompletion(lines, cursorLine, cursorCol, item, prefix) {
			const line = String(lines[cursorLine] ?? "");
			const start = Math.max(0, cursorCol - String(prefix ?? "").length);
			const next = [...lines];
			next[cursorLine] = line.slice(0, start) + "issue:" + item.value.slice(1) + line.slice(cursorCol);
			return { lines: next, cursorLine, cursorCol: start + 9 };
		},
	}));
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
	if providers := runtime.RegisteredAutocompleteProviders(); len(providers) != 1 {
		t.Fatalf("autocomplete providers=%d want 1", len(providers))
	}
	result, err := runtime.Autocomplete(context.Background(), AutocompleteRequest{
		Lines:      []string{"fix #1"},
		CursorLine: 0,
		CursorCol:  6,
		Input:      "fix #1",
		Cursor:     6,
	})
	if err != nil {
		t.Fatalf("autocomplete: %v", err)
	}
	if result.Prefix != "#1" || len(result.Items) != 1 || result.Items[0].Value != "#123" || result.Items[0].Description != "Fix login flow" {
		t.Fatalf("autocomplete result=%#v", result)
	}
	applied, ok, err := runtime.ApplyAutocomplete(context.Background(), AutocompleteApplyRequest{
		Lines:      []string{"fix #1"},
		CursorLine: 0,
		CursorCol:  6,
		Input:      "fix #1",
		Cursor:     6,
		Item:       result.Items[0],
		Prefix:     result.Prefix,
	})
	if err != nil || !ok {
		t.Fatalf("apply autocomplete ok=%v err=%v", ok, err)
	}
	if strings.Join(applied.Lines, "\n") != "fix issue:123" || applied.CursorCol != len("fix issue:123") {
		t.Fatalf("apply result=%#v", applied)
	}
}

func TestScriptExtensionAutocompleteCancellationAbortsSignal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "autocomplete-cancel-ext.mjs")
	source := `
export default function (pi) {
	let aborted = false;
	pi.addAutocompleteProvider(() => ({
		async getSuggestions(_lines, _cursorLine, _cursorCol, options) {
			await new Promise((resolve) => {
				options.signal.addEventListener("abort", () => {
					aborted = true;
					resolve();
				}, { once: true });
				setTimeout(resolve, 5000);
			});
			return { items: [{ value: "late" }] };
		},
	}));
	pi.registerTool({
		name: "abortstate",
		description: "report whether autocomplete was aborted",
		parameters: { type: "object", properties: {} },
		execute() {
			return { content: [{ type: "text", text: String(aborted) }] };
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := runtime.Autocomplete(ctx, AutocompleteRequest{Input: "slow", Lines: []string{"slow"}})
	cancel()
	if err == nil {
		t.Fatal("expected autocomplete to time out")
	}

	probe, ok := runtime.ToolDefinition("abortstate")
	if !ok {
		t.Fatal("abortstate tool missing")
	}
	deadline := time.Now().Add(time.Second)
	for {
		result, err := probe.Execute(context.Background(), []byte("{}"))
		if err != nil {
			t.Fatalf("execute probe: %v", err)
		}
		got := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
		if got == "true" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("autocomplete provider did not observe abort, got %q", got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestScriptExtensionContextCompatFields(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "ctx-ext.mjs")
	source := `
export default function (pi) {
	pi.registerTool({
		name: "ctxprobe",
		description: "report context compatibility fields",
		parameters: { type: "object", properties: {} },
		async execute(_id, _params, _a, _b, ctx) {
			ctx.abort();
			ctx.compact({
				customInstructions: "keep recent work",
				onComplete(result) { globalThis.compactSummary = result?.summary ?? "missing"; },
				onError(error) { globalThis.compactSummary = "error:" + error.message; },
			});
			const auth = await ctx.modelRegistry.getApiKeyAndHeaders(ctx.model);
			await new Promise((resolve) => setTimeout(resolve, 25));
			return { content: [{ type: "text", text: JSON.stringify({
				cwd: ctx.cwd,
				mode: ctx.mode,
				hasUI: ctx.hasUI,
				hasModelRegistry: !!ctx.modelRegistry,
				model: ctx.model?.provider + "/" + ctx.model?.id,
				allModels: ctx.modelRegistry.getAll().length,
				availableModels: ctx.modelRegistry.getAvailable().length,
				list: ctx.modelRegistry.list("faux").length,
				found: !!ctx.modelRegistry.find("faux", "faux"),
				hasAuth: ctx.modelRegistry.hasConfiguredAuth(ctx.model),
				authOK: auth?.ok === true,
				isIdle: ctx.isIdle(),
				hasPending: ctx.hasPendingMessages(),
				abortType: typeof ctx.abort,
				compactSummary: globalThis.compactSummary,
				systemPrompt: ctx.getSystemPrompt(),
				branch: ctx.sessionManager.getBranch().length,
				entries: ctx.sessionManager.getEntries().length,
				leaf: ctx.sessionManager.getLeafId(),
			}) }] };
		},
	});
	pi.on("session_before_compact", (payload, ctx) => {
		payload.branchCount = ctx.sessionManager.getBranch().length;
		return payload;
	});
}
`
	if err := os.WriteFile(ext, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	api := NewAPI()
	model := ai.Model{Provider: "faux", ID: "faux", Name: "Faux", API: "faux"}
	api.SetContextProvider(func() ExtensionContextSnapshot {
		return ExtensionContextSnapshot{
			CWD:                dir,
			Mode:               "tui",
			HasUI:              false,
			Model:              &model,
			Models:             []ai.Model{model, {Provider: "other", ID: "other", API: "faux"}},
			AvailableModels:    []ai.Model{model},
			IsIdle:             false,
			HasPendingMessages: true,
			SystemPrompt:       "system prompt",
			Entries:            []map[string]any{{"id": "root"}, {"id": "leaf"}},
			BranchEntries:      []map[string]any{{"id": "leaf"}},
			LeafID:             "leaf",
		}
	})
	var mu sync.Mutex
	var actions []string
	api.SetContextActionHandler(func(_ context.Context, action ExtensionContextAction) (json.RawMessage, error) {
		mu.Lock()
		actions = append(actions, action.Name)
		mu.Unlock()
		switch action.Name {
		case "compact":
			return json.RawMessage(`{"summary":"compacted"}`), nil
		case "getApiKeyAndHeaders":
			return json.RawMessage(`{"ok":true,"apiKey":"test"}`), nil
		default:
			return json.RawMessage("null"), nil
		}
	})
	runtime := NewRunnerWithAPI(api)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if errs := LoadScriptExtensions(context.Background(), runtime.API, []string{ext}, nil); len(errs) > 0 {
		t.Fatalf("load errors: %v", errs)
	}
	tool, ok := runtime.ToolDefinition("ctxprobe")
	if !ok {
		t.Fatalf("ctxprobe not registered; tools=%v", runtime.RegisteredTools())
	}
	result, err := tool.Execute(context.Background(), []byte("{}"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := ai.MessageText(ai.ToolResultMessage{Content: result.Content})
	for _, want := range []string{`"cwd":"` + dir + `"`, `"mode":"tui"`, `"hasUI":false`, `"hasModelRegistry":true`, `"model":"faux/faux"`, `"allModels":2`, `"availableModels":1`, `"list":1`, `"found":true`, `"hasAuth":true`, `"authOK":true`, `"isIdle":false`, `"hasPending":true`, `"abortType":"function"`, `"compactSummary":"compacted"`, `"systemPrompt":"system prompt"`, `"branch":1`, `"entries":2`, `"leaf":"leaf"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("context probe missing %s in %s", want, got)
		}
	}
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		joined := strings.Join(actions, ",")
		mu.Unlock()
		if strings.Contains(joined, "abort") && strings.Contains(joined, "compact") && strings.Contains(joined, "getApiKeyAndHeaders") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("context actions not called, got %q", joined)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	payload := &struct {
		Type          string           `json:"type"`
		BranchEntries []map[string]any `json:"BranchEntries"`
		BranchCount   int              `json:"branchCount"`
	}{
		Type:          "session_before_compact",
		BranchEntries: []map[string]any{{"id": "1"}, {"id": "2"}},
	}
	runtime.API.Emit("session_before_compact", payload)
	if payload.BranchCount != 1 {
		t.Fatalf("branchCount=%d want 1", payload.BranchCount)
	}
}
