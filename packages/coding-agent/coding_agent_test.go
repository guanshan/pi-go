package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

type probeTool struct{}

func (probeTool) Name() string           { return "probe" }
func (probeTool) Description() string    { return "probe tool" }
func (probeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (probeTool) Execute(context.Context, json.RawMessage, catools.ToolUpdate) ai.ToolResult {
	return ai.ToolResult{Content: ai.TextBlocks("ok")}
}

// TestCreateAgentSessionMergesCustomToolsThroughCore verifies the root facade
// routes through the core SDK: custom tools are merged on top of the builtin
// tool set rather than replacing it, and the core diagnostics/fallback fields
// are surfaced on the result.
func TestCreateAgentSessionMergesCustomToolsThroughCore(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, _, _ := registry.Match("faux", "faux")
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD:         t.TempDir(),
		AgentDir:    dir,
		Registry:    registry,
		Model:       model,
		CustomTools: core.ToolSet{"probe": probeTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := result.Session.Tools
	if _, ok := tools["probe"]; !ok {
		t.Fatalf("custom tool not merged: %v", toolNames(tools))
	}
	if _, ok := tools["read"]; !ok {
		t.Fatalf("builtin tools dropped when custom tools provided: %v", toolNames(tools))
	}
}

func toolNames(tools core.ToolSet) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

func TestCreateAgentSessionAndPrompt(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, _, _ := registry.Match("faux", "faux")
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD:           t.TempDir(),
		AgentDir:      dir,
		Registry:      registry,
		Model:         model,
		ThinkingLevel: ai.ThinkingOff,
		NoTools:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Session.Prompt(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	messages := result.Session.Session.BuildContext().Messages
	if got := ai.MessageText(messages[len(messages)-1]); got != "faux: hello" {
		t.Fatalf("text=%q", got)
	}
}

func TestRunRPCGetState(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, _, _ := registry.Match("faux", "faux")
	result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: t.TempDir(), AgentDir: dir, Registry: registry, Model: model, NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	resourceOptions := core.DefaultResourceLoaderOptions{NoContextFiles: true, NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true}
	runtime, err := core.CreateAgentSessionRuntime(context.Background(), func(ctx context.Context, options core.CreateAgentSessionRuntimeFactoryInput) (core.CreateAgentSessionRuntimeResult, error) {
		services, err := core.CreateAgentSessionServices(ctx, core.CreateAgentSessionServicesOptions{
			Cwd:                   options.Cwd,
			AgentDir:              options.AgentDir,
			ModelRegistry:         registry,
			ResourceLoaderOptions: resourceOptions,
		})
		if err != nil {
			return core.CreateAgentSessionRuntimeResult{}, err
		}
		created, err := core.CreateAgentSessionFromServices(ctx, core.CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: options.SessionManager,
			Model:          model,
			NoTools:        core.NoToolsAll,
		})
		if err != nil {
			return core.CreateAgentSessionRuntimeResult{}, err
		}
		return core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: created,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}, core.CreateAgentSessionRuntimeOptions{Cwd: result.Session.Session.CWD(), AgentDir: dir, SessionManager: result.Session.Session})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := core.RunRPC(context.Background(), runtime, bytes.NewBufferString(`{"id":"1","type":"get_state"}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"command":"get_state"`)) {
		t.Fatalf("unexpected rpc output: %s", out.String())
	}
}

func TestModelResolverAndEventBus(t *testing.T) {
	dir := t.TempDir()
	registry := ai.NewModelRegistry(dir, ai.NewAuthStorage(dir))
	model, thinking, err := ResolveCliModel(registry, "", "faux/faux:high")
	if err != nil {
		t.Fatal(err)
	}
	if model.Provider != "faux" || thinking != ai.ThinkingHigh {
		t.Fatalf("bad model resolution: %#v %s", model, thinking)
	}
	bus := NewEventBus()
	called := false
	bus.On("x", func(payload any) { called = payload.(string) == "ok" })
	bus.Emit("x", "ok")
	if !called {
		t.Fatal("event not delivered")
	}
	commands := BuiltinSlashCommands()
	foundImport := false
	for _, command := range commands {
		if command.Name == "import" {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Fatalf("builtin slash commands=%#v", commands)
	}
}

func TestBuildInfoAccessors(t *testing.T) {
	prevVersion := Version
	prevCommit := buildCommit
	prevDate := buildDate
	t.Cleanup(func() {
		Version = prevVersion
		buildCommit = prevCommit
		buildDate = prevDate
	})

	SetBuildInfo("1.2.3", "abc123", "2026-05-28")
	if got := BuildCommit(); got != "abc123" {
		t.Fatalf("commit=%q", got)
	}
	if got := BuildDate(); got != "2026-05-28" {
		t.Fatalf("date=%q", got)
	}
	if got := BuildVersion(); got != "1.2.3 commit abc123 built 2026-05-28" {
		t.Fatalf("version=%q", got)
	}
	SetBuildInfo("", "def456", "2026-05-29")
	if Version != "1.2.3" {
		t.Fatalf("version should remain unchanged, got %q", Version)
	}
}

func TestMainRunsMigrationsOnNormalStartup(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
	t.Setenv(core.EnvAgentDir, agentDir)

	session := `{"type":"session","cwd":"/root/project"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "s.jsonl"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Main(context.Background(), []string{"--print", "hello", "--model", "faux/faux", "--no-session", "--no-tools"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "sessions", "--root-project--", "s.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestMainSkipsMigrationsForVersion(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
	t.Setenv(core.EnvAgentDir, agentDir)

	if err := os.WriteFile(filepath.Join(agentDir, "s.jsonl"), []byte(`{"type":"session","cwd":"/root/project"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Main(context.Background(), []string{"--version"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "s.jsonl")); err != nil {
		t.Fatal(err)
	}
}

// TestMainRunsMigrationsForHelp mirrors TS main.ts ordering: runMigrations
// (main.ts:542) executes before printHelp (main.ts:690), so a `--help`
// invocation still runs startup migrations. A legacy session file at the agent
// root must be relocated into sessions/--<project>--/ even when only printing
// help. (--version, by contrast, exits before migrations.)
func TestMainRunsMigrationsForHelp(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
	t.Setenv(core.EnvAgentDir, agentDir)

	session := `{"type":"session","cwd":"/root/project"}` + "\n"
	if err := os.WriteFile(filepath.Join(agentDir, "s.jsonl"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Main(context.Background(), []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "sessions", "--root-project--", "s.jsonl")); err != nil {
		t.Fatalf("--help should run migrations and relocate the legacy session: %v", err)
	}
}

func TestHandleConfigCommandListsResources(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
	t.Setenv(core.EnvAgentDir, agentDir)
	if err := os.MkdirAll(filepath.Join(cwd, ConfigDirName, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ConfigDirName, "extensions", "demo.js"), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	handled, err := handleConfigCommand([]string{"config", "--list"}, bytes.NewBuffer(nil), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("config command was not handled")
	}
	text := out.String()
	if !strings.Contains(text, "Resource Configuration") || !strings.Contains(text, "extensions") || !strings.Contains(text, "demo.js") {
		t.Fatalf("unexpected config output:\n%s", text)
	}
}

func TestMainWithOptionsLoadsExtensionFactories(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
	t.Setenv(core.EnvAgentDir, agentDir)

	loaded := false
	err = MainWithOptions(context.Background(), []string{"--print", "hello", "--model", "faux/faux", "--no-session", "--no-tools"}, MainOptions{
		ExtensionFactories: []ExtensionFactory{
			func(api *ExtensionAPI) error {
				loaded = true
				api.RegisterCommand("inline", "Inline command")
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded {
		t.Fatal("expected MainWithOptions to load inline extension factories")
	}
}

func TestPackageManagerPathAndAuth(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgDir := t.TempDir()
	if err := os.WriteFile(pkgDir+"/README.md", []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := core.NewSettingsManager(cwd, agentDir)
	pm := NewDefaultPackageManager(cwd, agentDir, settings)
	record, err := pm.Install(pkgDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.Path != pkgDir {
		t.Fatalf("path=%q", record.Path)
	}
	if len(pm.List(false)) != 0 {
		t.Fatal("install should not persist package settings")
	}
	if err := pm.InstallAndPersist(pkgDir); err != nil {
		t.Fatal(err)
	}
	if len(pm.List(false)) != 1 {
		t.Fatal("package not listed")
	}
	auth := ai.NewAuthStorage(agentDir)
	if err := SaveAPIKey(auth, "faux", "key"); err != nil {
		t.Fatal(err)
	}
	status := GetAuthStatus(auth, ai.Model{Provider: "faux"})
	if !status.HasKey {
		t.Fatal("auth status missing key")
	}
	reloaded := ai.NewAuthStorage(agentDir)
	if got := reloaded.APIKey(ai.Model{Provider: "faux"}); got != "key" {
		t.Fatalf("reloaded key=%q", got)
	}
}
