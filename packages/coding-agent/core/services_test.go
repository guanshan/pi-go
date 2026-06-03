package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestCreateAgentSessionServicesLoadsResourcesAndDiagnostics(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	promptDir := filepath.Join(cwd, "prompts")
	appendPath := filepath.Join(cwd, "append.md")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "hello.md"), []byte("hello prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appendPath, []byte("append prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"broken":`), 0o644); err != nil {
		t.Fatal(err)
	}

	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:                true,
			NoExtensions:                  true,
			NoSkills:                      true,
			NoThemes:                      true,
			AdditionalPromptTemplatePaths: []string{promptDir},
			SystemPrompt:                  "overridden system prompt",
			AppendSystemPrompt:            []string{appendPath},
		},
		ExtensionFlagValues: map[string]any{"feature": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if services.Cwd != cwd || services.AgentDir != agentDir {
		t.Fatalf("services=%#v", services)
	}
	if services.ResourceLoaderOptions.SystemPrompt != "overridden system prompt" {
		t.Fatalf("loader options=%#v", services.ResourceLoaderOptions)
	}
	if _, ok := services.ResourceLoader.PromptTemplates["hello"]; !ok {
		t.Fatalf("prompt templates=%#v", services.ResourceLoader.PromptTemplates)
	}
	systemPrompt := services.ResourceLoader.BuildSystemPrompt(cliArgsForTest(), ToolPromptInfo{})
	if !strings.Contains(systemPrompt, "overridden system prompt") || !strings.Contains(systemPrompt, "append prompt") {
		t.Fatalf("system prompt=%q", systemPrompt)
	}
	if len(services.Diagnostics) < 1 {
		t.Fatalf("diagnostics=%#v", services.Diagnostics)
	}
	if services.Diagnostics[0].Type != DiagWarning {
		t.Fatalf("diagnostics=%#v", services.Diagnostics)
	}
}

func TestCreateAgentSessionFromServicesUsesScopedModelAndToolFilter(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	registry := ai.NewModelRegistry(agentDir, ai.NewAuthStorage(agentDir))
	faux, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		ModelRegistry: registry,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		ScopedModels: []ScopedModel{{
			Model:         faux,
			ThinkingLevel: ai.ThinkingHigh,
		}},
		Tools:   []string{"read", "grep"},
		NoTools: NoToolsNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session == nil {
		t.Fatal("missing session")
	}
	if created.Session.Model.Provider != "faux" || created.Session.ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("session model=%#v thinking=%s", created.Session.Model, created.Session.ThinkingLevel)
	}
	if len(created.Session.Tools) != 2 || created.Session.Tools["read"] == nil || created.Session.Tools["grep"] == nil {
		t.Fatalf("tools=%#v", created.Session.Tools)
	}
	if created.ModelFallbackMessage != "" {
		t.Fatalf("unexpected fallback=%q", created.ModelFallbackMessage)
	}
}

func TestCreateAgentSessionFromServicesRestoresThinkingOnlyWhenSessionHasThinkingChange(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.DefaultThinkingLevel = ai.ThinkingHigh
	registry := ai.NewModelRegistry(agentDir, ai.NewAuthStorage(agentDir))
	faux, _, _ := registry.Match("faux", "faux")
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settings,
		ModelRegistry:   registry,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	withoutThinking := InMemorySession(cwd)
	if err := withoutThinking.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services:       services,
		SessionManager: withoutThinking,
		Model:          faux,
		NoTools:        NoToolsAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("thinking without explicit session change=%s", created.Session.ThinkingLevel)
	}

	withThinking := InMemorySession(cwd)
	if err := withThinking.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	if err := withThinking.AppendThinkingChange(ai.ThinkingOff); err != nil {
		t.Fatal(err)
	}
	created, err = CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services:       services,
		SessionManager: withThinking,
		Model:          faux,
		NoTools:        NoToolsAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.ThinkingLevel != ai.ThinkingOff {
		t.Fatalf("thinking with explicit session change=%s", created.Session.ThinkingLevel)
	}
}

func TestCreateAgentSessionFromServicesExcludeTools(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	registry := ai.NewModelRegistry(agentDir, ai.NewAuthStorage(agentDir))
	faux, _, _ := registry.Match("faux", "faux")
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:           cwd,
		AgentDir:      agentDir,
		ModelRegistry: registry,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services:     services,
		ScopedModels: []ScopedModel{{Model: faux}},
		ExcludeTools: []string{"bash", "write"},
		NoTools:      NoToolsNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := created.Session.Tools
	if tools["bash"] != nil || tools["write"] != nil {
		t.Fatalf("excluded tools still present: %#v", tools)
	}
	if tools["read"] == nil || tools["edit"] == nil {
		t.Fatalf("non-excluded default tools missing: %#v", tools)
	}
}

func TestValidSessionID(t *testing.T) {
	valid := []string{"abc", "a", "fixed-id.1", "A_b-2.c", "9x9"}
	invalid := []string{"", "-abc", "abc-", ".x", "x.", "a b", "a/b", "a@b"}
	for _, id := range valid {
		if !ValidSessionID(id) {
			t.Errorf("expected %q valid", id)
		}
	}
	for _, id := range invalid {
		if ValidSessionID(id) {
			t.Errorf("expected %q invalid", id)
		}
	}
}

func TestNewSessionManagerWithID(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	sm, err := NewSessionManagerWithID(cwd, sessionDir, "fixed-id.1")
	if err != nil {
		t.Fatal(err)
	}
	if sm.SessionID() != "fixed-id.1" {
		t.Fatalf("sessionID=%q", sm.SessionID())
	}
}

func TestCreateAgentSessionCoreWrapper(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	result, err := CreateAgentSession(context.Background(), CreateAgentSessionOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		NoTools:  NoToolsAll,
		ScopedModels: []ScopedModel{{
			Model: ai.Model{Provider: "faux", ID: "faux", API: "faux"},
		}},
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session == nil {
		t.Fatal("missing session")
	}
	if len(result.Session.Tools) != 0 {
		t.Fatalf("tools=%#v", result.Session.Tools)
	}
	if err := result.Session.Prompt(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := ai.MessageText(result.Session.Session.BuildContext().Messages[1]); got != "faux: hello" {
		t.Fatalf("text=%q", got)
	}
}

func TestCreateAgentSessionFromServicesIncludesExtensionTools(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			ExtensionFactories: []coreext.Factory{
				func(api *coreext.API) error {
					api.RegisterTool(coreext.DefineTool("inline", "Inline tool", map[string]any{"type": "object"}, func(context.Context, []byte) (ai.ToolResult, error) {
						return ai.ToolResult{Content: ai.TextBlocks("ok")}, nil
					}))
					return nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if services.ExtensionRuntime == nil {
		t.Fatal("missing extension runtime")
	}
	if _, ok := services.ExtensionRuntime.ToolDefinition("inline"); !ok {
		t.Fatal("inline tool not registered in runtime")
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		NoTools:  NoToolsNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.Tools["inline"] == nil {
		t.Fatalf("tools=%#v", created.Session.Tools)
	}
}

func TestCreateAgentSessionServicesLoadsScriptExtensionTool(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("requires node for script extension loading")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extensionPath := filepath.Join(cwd, "hello.ts")
	if err := os.WriteFile(extensionPath, []byte(`
import { Type } from "@earendil-works/pi-ai";
import { defineTool } from "@earendil-works/pi-coding-agent";

export default function (pi) {
	pi.registerTool(defineTool({
		name: "hello",
		label: "Hello",
		description: "Say hello",
		parameters: Type.Object({
			name: Type.String({ description: "Name to greet" }),
		}),
		async execute(_toolCallId, params) {
			return {
				content: [{ type: "text", text: "Hello, " + params.name + "!" }],
				details: { greeted: params.name },
			};
		},
	}));
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:           true,
			NoExtensions:             true,
			NoSkills:                 true,
			NoPromptTemplates:        true,
			NoThemes:                 true,
			AdditionalExtensionPaths: []string{extensionPath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range services.Diagnostics {
		if diagnostic.Type == DiagError {
			t.Fatalf("diagnostics=%#v", services.Diagnostics)
		}
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		NoTools:  NoToolsBuiltin,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Session.Dispose()
	tool := created.Session.Tools["hello"]
	if tool == nil {
		t.Fatalf("tools=%#v", created.Session.Tools)
	}
	result := tool.Execute(context.Background(), raw(map[string]any{"name": "Ada"}), nil)
	if result.IsError {
		t.Fatalf("tool error=%s", toolText(result.Content))
	}
	if got := toolText(result.Content); got != "Hello, Ada!" {
		t.Fatalf("tool output=%q", got)
	}
}

func TestScriptExtensionBeforeHookMutationReturnsToGo(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("requires node for script extension loading")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extensionPath := filepath.Join(cwd, "cancel-switch.mjs")
	if err := os.WriteFile(extensionPath, []byte(`
export default function (pi) {
	pi.on("session_before_switch", (event) => {
		event.cancel = true;
	});
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:           true,
			NoExtensions:             true,
			NoSkills:                 true,
			NoPromptTemplates:        true,
			NoThemes:                 true,
			AdditionalExtensionPaths: []string{extensionPath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		NoTools:  NoToolsAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Session.Dispose()
	if !created.Session.shouldCancelSessionSwitch(coreext.SessionStartResume, "target.jsonl") {
		t.Fatal("script extension mutation did not cancel session switch")
	}
}

func TestScriptExtensionSlashCommandExecutes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("requires node for script extension loading")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extensionPath := filepath.Join(cwd, "command.mjs")
	if err := os.WriteFile(extensionPath, []byte(`
export default function (pi) {
	pi.registerCommand("hello", {
		description: "Say hello",
		async handler(args, ctx) {
			return "Hello, " + args + " (ui=" + String(ctx.hasUI) + ")";
		},
	});
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:           true,
			NoExtensions:             true,
			NoSkills:                 true,
			NoPromptTemplates:        true,
			NoThemes:                 true,
			AdditionalExtensionPaths: []string{extensionPath},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range services.Diagnostics {
		if diagnostic.Type == DiagError {
			t.Fatalf("diagnostics=%#v", services.Diagnostics)
		}
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		NoTools:  NoToolsAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Session.Dispose()

	var stdout, stderr strings.Builder
	done, err := handleSlash(context.Background(), created.Session, "/hello Ada", &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("extension command should not quit")
	}
	if got := strings.TrimSpace(stdout.String()); got != "Hello, Ada (ui=false)" {
		t.Fatalf("stdout=%q stderr=%q", got, stderr.String())
	}
}

func cliArgsForTest() cli.Args {
	return cli.Args{}
}

// TestExtensionFlagsInProcess verifies an in-process extension can declare a CLI
// flag, that the flag appears in help, and that a host-supplied value is injected.
func TestExtensionFlagsInProcess(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
			NoThemes:          true,
			ExtensionFactories: []coreext.Factory{
				func(api *coreext.API) error {
					api.RegisterFlag(coreext.FlagDefinition{Name: "preset", Description: "named preset", Type: "string", Default: "base"})
					return nil
				},
			},
		},
		ExtensionFlagValues: map[string]any{"preset": "fast"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := services.ExtensionRuntime.FlagValue("preset"); got != "fast" {
		t.Fatalf("flag value=%v, want fast", got)
	}
	help := extensionFlagHelp(services.ExtensionRuntime)
	if len(help) != 1 || !strings.Contains(help[0], "preset") || !strings.Contains(help[0], "named preset") {
		t.Fatalf("extension flag help=%#v", help)
	}
}

// TestScriptExtensionFlags verifies a script extension can declare a CLI flag,
// read its injected value via getFlag, and have it surfaced for help.
func TestScriptExtensionFlags(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("requires node for script extension loading")
	}
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extensionPath := filepath.Join(cwd, "preset.ts")
	if err := os.WriteFile(extensionPath, []byte(`
import { Type } from "@earendil-works/pi-ai";
import { defineTool } from "@earendil-works/pi-coding-agent";

export default function (pi) {
	pi.registerFlag("preset", { description: "named preset", type: "string", default: "base" });
	pi.registerTool(defineTool({
		name: "current_preset",
		label: "Current preset",
		description: "Return the active preset flag value",
		parameters: Type.Object({}),
		async execute() {
			return { content: [{ type: "text", text: String(pi.getFlag("preset")) }], details: {} };
		},
	}));
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:      cwd,
		AgentDir: agentDir,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:           true,
			NoExtensions:             true,
			NoSkills:                 true,
			NoPromptTemplates:        true,
			NoThemes:                 true,
			AdditionalExtensionPaths: []string{extensionPath},
		},
		ExtensionFlagValues: map[string]any{"preset": "fast"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range services.Diagnostics {
		if diagnostic.Type == DiagError {
			t.Fatalf("diagnostics=%#v", services.Diagnostics)
		}
	}
	if help := extensionFlagHelp(services.ExtensionRuntime); len(help) != 1 || !strings.Contains(help[0], "preset") {
		t.Fatalf("extension flag help=%#v", help)
	}
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		NoTools:  NoToolsBuiltin,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer created.Session.Dispose()
	tool := created.Session.Tools["current_preset"]
	if tool == nil {
		t.Fatalf("tools=%#v", created.Session.Tools)
	}
	result := tool.Execute(context.Background(), raw(map[string]any{}), nil)
	if result.IsError {
		t.Fatalf("tool error=%s", toolText(result.Content))
	}
	if got := toolText(result.Content); got != "fast" {
		t.Fatalf("getFlag returned %q, want fast (injected CLI value)", got)
	}
}
