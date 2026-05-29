package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

func TestDefaultResourceLoaderLoadsTSStyleResources(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace", "app")
	agentDir := filepath.Join(root, "agent")
	writeTestFile(t, filepath.Join(agentDir, "AGENTS.md"), "global agents")
	writeTestFile(t, filepath.Join(root, "workspace", "AGENTS.MD"), "project agents")
	writeTestFile(t, filepath.Join(cwd, ConfigDirName, "SYSTEM.md"), "project system")
	writeTestFile(t, filepath.Join(agentDir, "APPEND_SYSTEM.md"), "global append")
	writeTestFile(t, filepath.Join(cwd, ConfigDirName, "APPEND_SYSTEM.md"), "project append")
	writeTestFile(t, filepath.Join(agentDir, "prompts", "foo.md"), "---\ntitle: Foo\n---\nfoo prompt")
	writeTestFile(t, filepath.Join(agentDir, "skills", "demo", "SKILL.md"), "# Demo\nUse this skill for demos.")
	writeTestFile(t, filepath.Join(agentDir, "themes", "quiet.json"), `{"name":"quiet"}`)
	writeTestFile(t, filepath.Join(agentDir, "extensions", "one.js"), "export default {};")

	loader, err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}

	agentsFiles := loader.GetAgentsFiles().AgentsFiles
	if len(agentsFiles) != 2 {
		t.Fatalf("agents files=%#v", agentsFiles)
	}
	if agentsFiles[0].Path != filepath.Join(agentDir, "AGENTS.md") || agentsFiles[0].Content != "global agents" {
		t.Fatalf("global context not first: %#v", agentsFiles)
	}
	if agentsFiles[1].Path != filepath.Join(root, "workspace", "AGENTS.MD") {
		t.Fatalf("uppercase ancestor context not loaded: %#v", agentsFiles)
	}

	if prompt := loader.GetSystemPrompt(); prompt == nil || *prompt != "project system" {
		t.Fatalf("system prompt=%v", prompt)
	}
	if appendPrompt := loader.GetAppendSystemPrompt(); len(appendPrompt) != 1 || appendPrompt[0] != "project append" {
		t.Fatalf("append prompt=%#v", appendPrompt)
	}
	if !hasPrompt(loader.GetPrompts().Prompts, "foo", "foo prompt") {
		t.Fatalf("prompts=%#v", loader.GetPrompts().Prompts)
	}
	if !hasSkill(loader.GetSkills().Skills, "demo") {
		t.Fatalf("skills=%#v", loader.GetSkills().Skills)
	}
	if !hasTheme(loader.GetThemes().Themes, "quiet") {
		t.Fatalf("themes=%#v", loader.GetThemes().Themes)
	}
	if !hasExtension(loader.GetExtensions().Extensions, filepath.Join(agentDir, "extensions", "one.js")) {
		t.Fatalf("extensions=%#v", loader.GetExtensions().Extensions)
	}
}

func TestDefaultResourceLoaderAdditionalPathsFactoriesAndExtend(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	external := filepath.Join(cwd, "external")
	writeTestFile(t, filepath.Join(agentDir, "skills", "default", "SKILL.md"), "default skill")
	writeTestFile(t, filepath.Join(external, "special", "SKILL.md"), "special skill")
	writeTestFile(t, filepath.Join(external, "prompts", "ask.md"), "ask prompt")
	writeTestFile(t, filepath.Join(external, "theme.json"), `{"name":"theme"}`)
	writeTestFile(t, filepath.Join(external, "ext.mjs"), "export default {};")

	bus := NewEventBus()
	emitted := false
	bus.On("inline-loaded", func(any) { emitted = true })
	loader, err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD:                           cwd,
		AgentDir:                      agentDir,
		EventBus:                      bus,
		NoExtensions:                  true,
		NoSkills:                      true,
		NoPromptTemplates:             true,
		NoThemes:                      true,
		AdditionalExtensionPaths:      []string{filepath.Join(external, "ext.mjs")},
		AdditionalSkillPaths:          []string{filepath.Join(external, "special"), filepath.Join(external, "missing-skill")},
		AdditionalPromptTemplatePaths: []string{filepath.Join(external, "prompts")},
		AdditionalThemePaths:          []string{filepath.Join(external, "theme.json")},
		ExtensionFactories: []ExtensionFactory{
			func(api *ExtensionAPI) error {
				api.RegisterCommand("inline", "Inline command")
				api.Emit("inline-loaded", true)
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("inline extension did not receive the configured event bus")
	}
	if hasSkill(loader.GetSkills().Skills, "default") || !hasSkill(loader.GetSkills().Skills, "special") {
		t.Fatalf("skills=%#v", loader.GetSkills().Skills)
	}
	if !diagnosticsContain(loader.GetSkills().Diagnostics, "Skill path does not exist") {
		t.Fatalf("skill diagnostics=%#v", loader.GetSkills().Diagnostics)
	}
	if !hasPrompt(loader.GetPrompts().Prompts, "ask", "ask prompt") {
		t.Fatalf("prompts=%#v", loader.GetPrompts().Prompts)
	}
	if !hasTheme(loader.GetThemes().Themes, "theme") {
		t.Fatalf("themes=%#v", loader.GetThemes().Themes)
	}
	if !hasExtension(loader.GetExtensions().Extensions, filepath.Join(external, "ext.mjs")) {
		t.Fatalf("extensions=%#v", loader.GetExtensions().Extensions)
	}
	if !hasInlineCommand(loader.GetExtensions().Extensions, "inline") {
		t.Fatalf("inline extension not loaded: %#v", loader.GetExtensions().Extensions)
	}

	extensionRoot := filepath.Join(external, "pack")
	writeTestFile(t, filepath.Join(extensionRoot, "new-skill", "SKILL.md"), "new skill")
	writeTestFile(t, filepath.Join(extensionRoot, "prompts", "later.md"), "later prompt")
	writeTestFile(t, filepath.Join(extensionRoot, "themes", "later.json"), `{"name":"later"}`)
	metadata := PathMetadata{Source: "inline", Scope: "temporary", Origin: "top-level", BaseDir: extensionRoot}
	loader.ExtendResources(ResourceExtensionPaths{
		SkillPaths:  []ResourcePathEntry{{Path: "new-skill", Metadata: metadata}},
		PromptPaths: []ResourcePathEntry{{Path: "prompts", Metadata: metadata}},
		ThemePaths:  []ResourcePathEntry{{Path: "themes", Metadata: metadata}},
	})

	if !hasSkill(loader.GetSkills().Skills, "new-skill") {
		t.Fatalf("extended skills=%#v", loader.GetSkills().Skills)
	}
	if !hasPrompt(loader.GetPrompts().Prompts, "later", "later prompt") {
		t.Fatalf("extended prompts=%#v", loader.GetPrompts().Prompts)
	}
	if !hasTheme(loader.GetThemes().Themes, "later") {
		t.Fatalf("extended themes=%#v", loader.GetThemes().Themes)
	}
}

func TestDefaultResourceLoaderUsesPackageManagerResolvedResources(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	settings := core.NewSettingsManager(cwd, agentDir)
	settings.Project.DisabledExtensions = []string{"skip.js"}
	packageDir := filepath.Join(cwd, "manifest-package")
	writeTestFile(t, filepath.Join(packageDir, "package.json"), `{"pi":{"prompts":["custom.md"],"skills":["skill/SKILL.md"]}}`)
	writeTestFile(t, filepath.Join(packageDir, "custom.md"), "custom package prompt")
	writeTestFile(t, filepath.Join(packageDir, "skill", "SKILL.md"), "custom package skill")
	writeTestFile(t, filepath.Join(cwd, ConfigDirName, "extensions", "skip.js"), "disabled extension")
	settings.Project.InstalledPackages = []core.PackageRecord{{Source: "manifest-package", Path: packageDir, Local: true}}

	loader, err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPrompt(loader.GetPrompts().Prompts, "custom", "custom package prompt") {
		t.Fatalf("prompts=%#v", loader.GetPrompts().Prompts)
	}
	if !hasSkill(loader.GetSkills().Skills, "skill") {
		t.Fatalf("skills=%#v", loader.GetSkills().Skills)
	}
	if hasExtension(loader.GetExtensions().Extensions, filepath.Join(cwd, ConfigDirName, "extensions", "skip.js")) {
		t.Fatalf("disabled extension was loaded: %#v", loader.GetExtensions().Extensions)
	}
}

func TestDefaultResourceLoaderReturnsSnapshotsAndWiresRuntimeShutdown(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	writeTestFile(t, filepath.Join(agentDir, "extensions", "one.js"), "export default {};")
	writeTestFile(t, filepath.Join(agentDir, "APPEND_SYSTEM.md"), "append")
	writeTestFile(t, filepath.Join(agentDir, "SYSTEM.md"), "system")

	var calls []string
	loader, err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD:      cwd,
		AgentDir: agentDir,
		ExtensionFactories: []ExtensionFactory{
			func(api *ExtensionAPI) error {
				api.OnShutdown(func(context.Context) error {
					calls = append(calls, "inline")
					return nil
				})
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	extensions := loader.GetExtensions()
	if len(extensions.Extensions) == 0 {
		t.Fatalf("extensions=%#v", extensions.Extensions)
	}
	originalPath := extensions.Extensions[0].Path
	extensions.Extensions[0].Path = "mutated"
	if loader.GetExtensions().Extensions[0].Path != originalPath {
		t.Fatalf("extension snapshot leaked mutation")
	}

	appendPrompt := loader.GetAppendSystemPrompt()
	appendPrompt[0] = "mutated"
	if loader.GetAppendSystemPrompt()[0] != "append" {
		t.Fatalf("append prompt snapshot leaked mutation")
	}

	systemPrompt := loader.GetSystemPrompt()
	if systemPrompt == nil {
		t.Fatal("system prompt missing")
	}
	*systemPrompt = "mutated"
	if got := loader.GetSystemPrompt(); got == nil || *got != "system" {
		t.Fatalf("system prompt snapshot leaked mutation: %v", got)
	}

	if err := loader.GetExtensions().Runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "inline" {
		t.Fatalf("shutdown calls=%#v", calls)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasSkill(skills []core.Skill, name string) bool {
	for _, skill := range skills {
		if skill.Name == name {
			return true
		}
	}
	return false
}

func hasPrompt(prompts []core.PromptTemplate, name, content string) bool {
	for _, prompt := range prompts {
		if prompt.Name == name && strings.TrimSpace(prompt.Content) == content {
			return true
		}
	}
	return false
}

func hasTheme(themes []Theme, name string) bool {
	for _, theme := range themes {
		if theme.Name == name {
			return true
		}
	}
	return false
}

func hasExtension(extensions []LoadedExtension, path string) bool {
	for _, extension := range extensions {
		if extension.Path == path {
			return true
		}
	}
	return false
}

func hasInlineCommand(extensions []LoadedExtension, name string) bool {
	for _, extension := range extensions {
		for _, command := range extension.Commands {
			if command.Name == name {
				return true
			}
		}
	}
	return false
}

func diagnosticsContain(diagnostics []cli.Diagnostic, text string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, text) {
			return true
		}
	}
	return false
}
