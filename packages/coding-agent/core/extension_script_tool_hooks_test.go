package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func mustField(t *testing.T, raw json.RawMessage, key string) any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode args %s: %v", raw, err)
	}
	return decoded[key]
}

// writeScriptExtension writes an ES-module extension to a temp file and returns
// its path. Plain JS avoids depending on Node's experimental TS type-stripping.
func writeScriptExtension(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "extension.mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadScriptRuntime(t *testing.T, path string) *coreext.Runner {
	t.Helper()
	runtime := coreext.NewRunnerWithAPI(coreext.NewAPI())
	errs := coreext.LoadScriptExtensions(context.Background(), runtime.API, []string{path}, nil)
	for _, err := range errs {
		if err != nil {
			t.Fatalf("load script extension: %v", err)
		}
	}
	return runtime
}

// TestScriptExtensionBlocksToolCall drives a real Node script extension whose
// tool_call handler blocks dangerous bash commands (the permission-gate pattern).
// The tool must not execute and the result must carry the block reason.
func TestScriptExtensionBlocksToolCall(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	source := `export default function (pi) {
		pi.on("tool_call", (event) => {
			if (event.toolName === "bash" && /\brm\s+-rf\b/.test(event.input.command || "")) {
				return { block: true, reason: "Dangerous command blocked" };
			}
			return undefined;
		});
	}`
	path := writeScriptExtension(t, source)
	runtime := loadScriptRuntime(t, path)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
	if !runtime.HasHandlers("tool_call") {
		t.Fatal("expected tool_call handler to be registered")
	}

	server, _ := toolCallProvider(t, "bash", `{"command":"rm -rf /tmp/x"}`)
	tool := &recordingTool{name: "bash"}
	agent := scriptHookAgent(t, server, tool, runtime)

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.executed != 0 {
		t.Fatalf("blocked tool executed %d times", tool.executed)
	}
	var toolResultText string
	for _, m := range agent.Session.BuildContext().Messages {
		if ai.MessageRole(m) == "toolResult" {
			toolResultText = ai.MessageText(m)
		}
	}
	if toolResultText != "Dangerous command blocked" {
		t.Fatalf("tool result text=%q", toolResultText)
	}
}

// TestScriptExtensionMutatesToolInput drives a Node script extension whose
// tool_call handler mutates event.input in place; the executed args must change.
func TestScriptExtensionMutatesToolInput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	source := `export default function (pi) {
		pi.on("tool_call", (event) => {
			if (event.toolName === "write") {
				event.input.path = "mutated.txt";
			}
			return undefined;
		});
	}`
	path := writeScriptExtension(t, source)
	runtime := loadScriptRuntime(t, path)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	server, _ := toolCallProvider(t, "write", `{"path":"original.txt","command":"x"}`)
	tool := &recordingTool{name: "write"}
	agent := scriptHookAgent(t, server, tool, runtime)

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if tool.executed != 1 {
		t.Fatalf("tool executed %d times", tool.executed)
	}
	if got := mustField(t, tool.rawArgs, "path"); got != "mutated.txt" {
		t.Fatalf("executed path=%v want mutated.txt (raw=%s)", got, tool.rawArgs)
	}
}

func TestScriptExtensionToolResultSeesMutatedInput(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	source := `export default function (pi) {
		pi.on("tool_call", (event) => {
			if (event.toolName === "write") {
				event.input.path = "mutated.txt";
			}
			return undefined;
		});
		pi.on("tool_result", (event) => {
			return { content: [{ type: "text", text: "result-input:" + event.input.path }] };
		});
	}`
	path := writeScriptExtension(t, source)
	runtime := loadScriptRuntime(t, path)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	server, _ := toolCallProvider(t, "write", `{"path":"original.txt","command":"x"}`)
	tool := &recordingTool{name: "write"}
	agent := scriptHookAgent(t, server, tool, runtime)

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustField(t, tool.rawArgs, "path"); got != "mutated.txt" {
		t.Fatalf("executed path=%v want mutated.txt (raw=%s)", got, tool.rawArgs)
	}
	var resultText string
	for _, m := range agent.Session.BuildContext().Messages {
		if ai.MessageRole(m) == "toolResult" {
			resultText = ai.MessageText(m)
		}
	}
	if resultText != "result-input:mutated.txt" {
		t.Fatalf("tool_result input text=%q", resultText)
	}
}

// TestScriptExtensionOverridesToolResult drives a Node script extension whose
// tool_result handler overrides the content and isError of an executed tool.
func TestScriptExtensionOverridesToolResult(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	source := `export default function (pi) {
		pi.on("tool_result", (event) => {
			return { content: [{ type: "text", text: "redacted" }], isError: true };
		});
	}`
	path := writeScriptExtension(t, source)
	runtime := loadScriptRuntime(t, path)
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	server, _ := toolCallProvider(t, "lookup", `{"command":"x"}`)
	tool := &recordingTool{name: "lookup", result: ai.ToolResult{Content: ai.TextBlocks("secret output")}}
	agent := scriptHookAgent(t, server, tool, runtime)

	if err := agent.Prompt(context.Background(), "go", nil, nil); err != nil {
		t.Fatal(err)
	}
	var tr ai.ToolResultMessage
	found := false
	for _, m := range agent.Session.BuildContext().Messages {
		if v, ok := m.(ai.ToolResultMessage); ok {
			tr, found = v, true
		}
	}
	if !found {
		t.Fatal("no tool result message")
	}
	if ai.MessageText(tr) != "redacted" || !tr.IsError {
		t.Fatalf("tool result=%q isError=%v", ai.MessageText(tr), tr.IsError)
	}
}

// TestScriptExtensionUnsupportedAPIsFailFast verifies that calling an
// unsupported bridge API (registerProvider / registerMessageRenderer /
// addAutocompleteProvider) fails loading with a clear error instead of silently
// being undefined.
// TestScriptExtensionUnsupportedAPIsDegradeGracefully verifies that the
// unsupported registration APIs warn and skip instead of failing the whole
// extension load (4.md): the extension still loads and any tools it registers
// after the unsupported call survive.
func TestScriptExtensionUnsupportedAPIsDegradeGracefully(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	cases := []struct {
		name string
		call string
	}{
		{"registerProvider", `pi.registerProvider("x", {});`},
		{"registerMessageRenderer", `pi.registerMessageRenderer("x", {});`},
		{"addAutocompleteProvider", `pi.addAutocompleteProvider(() => {});`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "export default function (pi) {\n\t" + tc.call + "\n\t" +
				`pi.registerTool({ name: "survivor", description: "d", parameters: { type: "object", properties: {} }, execute() { return { content: [] }; } });` +
				"\n}"
			path := writeScriptExtension(t, src)
			runtime := coreext.NewRunnerWithAPI(coreext.NewAPI())
			errs := coreext.LoadScriptExtensions(context.Background(), runtime.API, []string{path}, nil)
			t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })
			if len(errs) != 0 {
				t.Fatalf("expected graceful load (no errors) for %s, got: %v", tc.name, errs)
			}
			found := false
			for _, tool := range runtime.API.SnapshotTools() {
				if tool.Name == "survivor" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("tool registered after %s was lost; tools=%v", tc.name, runtime.API.SnapshotTools())
			}
		})
	}
}

func scriptHookAgent(t *testing.T, server *httptest.Server, tool *recordingTool, runtime *coreext.Runner) *AgentSession {
	t.Helper()
	cwd := t.TempDir()
	settings := NewSettingsManager(cwd, t.TempDir())
	registry := ai.NewModelRegistry(settings.AgentDir, ai.NewAuthStorage(settings.AgentDir))
	registry.Auth.SetRuntime("openai", "test-key")
	model := ai.Model{Provider: "openai", ID: "gpt-test", API: "openai-completions", BaseURL: server.URL}
	session := InMemorySession(cwd)
	resources := ResourceLoader{CWD: cwd, AgentDir: settings.AgentDir, PromptTemplates: map[string]PromptTemplate{}, Skills: map[string]Skill{}}
	agent := NewAgentSession(session, settings, registry, resources, model, ai.ThinkingOff, ToolSet{tool.Name(): tool}, "system")
	agent.extensionRuntime = runtime
	return agent
}
