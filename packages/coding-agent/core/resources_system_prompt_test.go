package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

// toolInfo builds a ToolPromptInfo for the named builtin tools (with their real
// snippets/guidelines), for tests that only care about which tools are present.
func toolInfo(names ...string) ToolPromptInfo {
	builtins := BuiltinTools("/tmp", nil)
	set := ToolSet{}
	for _, n := range names {
		if tool, ok := builtins[n]; ok {
			set[n] = tool
		}
	}
	return ToolPromptInfoFor(set)
}

// writeFileT writes content to path, creating parent dirs, failing the test on error.
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadResourcesAppendSystemSingleSelectProjectWins verifies that when both
// the global and project APPEND_SYSTEM.md exist, only the project one is used
// (single-select, project-over-global) — TS resource-loader.ts:867.
func TestLoadResourcesAppendSystemSingleSelectProjectWins(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	writeFileT(t, filepath.Join(agentDir, "APPEND_SYSTEM.md"), "GLOBAL APPEND")
	writeFileT(t, filepath.Join(ProjectPiDir(cwd), "APPEND_SYSTEM.md"), "PROJECT APPEND")

	loader := LoadResources(cwd, agentDir, cli.Args{
		NoContextFiles: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoExtensions: true,
	}, nil)

	if strings.TrimSpace(loader.AppendPrompt) != "PROJECT APPEND" {
		t.Fatalf("AppendPrompt = %q, want only project append", loader.AppendPrompt)
	}

	prompt := loader.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	if !strings.Contains(prompt, "PROJECT APPEND") {
		t.Fatalf("prompt missing project append: %q", prompt)
	}
	if strings.Contains(prompt, "GLOBAL APPEND") {
		t.Fatalf("prompt should not contain global append: %q", prompt)
	}
}

// TestLoadResourcesAppendSystemFallsBackToGlobal verifies that when only the
// global APPEND_SYSTEM.md exists it is still used.
func TestLoadResourcesAppendSystemFallsBackToGlobal(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	writeFileT(t, filepath.Join(agentDir, "APPEND_SYSTEM.md"), "GLOBAL ONLY APPEND")

	loader := LoadResources(cwd, agentDir, cli.Args{
		NoContextFiles: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoExtensions: true,
	}, nil)

	if strings.TrimSpace(loader.AppendPrompt) != "GLOBAL ONLY APPEND" {
		t.Fatalf("AppendPrompt = %q, want global append", loader.AppendPrompt)
	}
}

// TestBuildSystemPromptSkillsGatedOnReadTool verifies skills are listed only
// when the read tool is available — TS system-prompt.ts:164 — and that they use
// the <available_skills> XML shape from formatSkillsForPrompt (skills.ts).
func TestBuildSystemPromptSkillsGatedOnReadTool(t *testing.T) {
	loader := ResourceLoader{
		CWD:    "/tmp/work",
		Skills: map[string]Skill{"demo": {Name: "demo", Path: "/skills/demo/SKILL.md", Description: "do a demo"}},
	}

	withRead := loader.BuildSystemPrompt(cli.Args{}, toolInfo("grep", "read"))
	for _, want := range []string{
		"<available_skills>",
		"<name>demo</name>",
		"<description>do a demo</description>",
		"<location>/skills/demo/SKILL.md</location>",
		"</available_skills>",
	} {
		if !strings.Contains(withRead, want) {
			t.Fatalf("prompt with read tool missing %q:\n%s", want, withRead)
		}
	}

	withoutRead := loader.BuildSystemPrompt(cli.Args{}, toolInfo("grep"))
	if strings.Contains(withoutRead, "<available_skills>") || strings.Contains(withoutRead, "<name>demo</name>") {
		t.Fatalf("prompt without read tool should not list skills: %q", withoutRead)
	}
}

// TestBuildSystemPromptSkillsXMLEscaped verifies the skills block escapes XML
// entities exactly as TS escapeXml (&apos;/&quot;), not Go's numeric entities.
func TestBuildSystemPromptSkillsXMLEscaped(t *testing.T) {
	loader := ResourceLoader{
		CWD:    "/tmp/work",
		Skills: map[string]Skill{"q": {Name: "a&b", Path: "/p", Description: `say "hi" <now> it's`}},
	}
	prompt := loader.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	want := "<description>say &quot;hi&quot; &lt;now&gt; it&apos;s</description>"
	if !strings.Contains(prompt, want) {
		t.Fatalf("skills XML escaping mismatch, want %q in:\n%s", want, prompt)
	}
	if !strings.Contains(prompt, "<name>a&amp;b</name>") {
		t.Fatalf("skill name not escaped: %s", prompt)
	}
}

// TestBuildSystemPromptProjectContextXML verifies project context files are
// injected as a <project_context>/<project_instructions> XML block (TS
// buildSystemPrompt system-prompt.ts:58-67), not markdown headings.
func TestBuildSystemPromptProjectContextXML(t *testing.T) {
	cwd := t.TempDir()
	ctxFile := filepath.Join(cwd, "AGENTS.md")
	writeFileT(t, ctxFile, "do the thing")
	loader := ResourceLoader{CWD: cwd, ContextFiles: []string{ctxFile}}

	prompt := loader.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	for _, want := range []string{
		"<project_context>",
		"Project-specific instructions and guidelines:",
		"<project_instructions path=\"" + ctxFile + "\">",
		"do the thing",
		"</project_instructions>",
		"</project_context>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("project context XML missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "## Project Context Files") {
		t.Fatalf("project context should use XML, not markdown heading:\n%s", prompt)
	}

	// --no-context-files suppresses the block entirely.
	suppressed := loader.BuildSystemPrompt(cli.Args{NoContextFiles: true}, toolInfo("read"))
	if strings.Contains(suppressed, "<project_context>") {
		t.Fatalf("--no-context-files should omit the block:\n%s", suppressed)
	}
}

// TestBuildSystemPromptIncludesDateAndCwd verifies that both the default prompt
// and a custom prompt include the current date and working directory, appended
// last every turn — TS system-prompt.ts:168.
func TestBuildSystemPromptIncludesDateAndCwd(t *testing.T) {
	date := time.Now().Format("2006-01-02")
	cwd := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCwd := "Current working directory: " + filepath.ToSlash(cwd)
	wantDate := "Current date: " + date

	// Default prompt.
	def := ResourceLoader{CWD: cwd}.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	if !strings.Contains(def, wantDate) || !strings.Contains(def, wantCwd) {
		t.Fatalf("default prompt missing date/cwd: %q", def)
	}
	if !strings.HasSuffix(strings.TrimSpace(def), wantCwd) {
		t.Fatalf("default prompt should end with cwd line: %q", def)
	}

	// Custom prompt (provided via args).
	custom := ResourceLoader{CWD: cwd}.BuildSystemPrompt(cli.Args{SystemPrompt: "MY CUSTOM PROMPT"}, toolInfo("read"))
	if !strings.Contains(custom, "MY CUSTOM PROMPT") {
		t.Fatalf("custom prompt missing custom text: %q", custom)
	}
	if !strings.Contains(custom, wantDate) || !strings.Contains(custom, wantCwd) {
		t.Fatalf("custom prompt missing date/cwd: %q", custom)
	}
}

// TestBuildSystemPromptDefaultGoldenShape locks the byte-shape of the default
// prompt for the TS default tool set [read,bash,edit,write] against TS
// buildSystemPrompt (system-prompt.ts:130-147): lead paragraph, the one-line
// "Available tools:" snippet list, the deduped "Guidelines:" section (bash-only
// rule first, per-tool guidelines in registration order, always-on bullets last),
// and the "Pi documentation:" block with absolute doc paths.
func TestBuildSystemPromptDefaultGoldenShape(t *testing.T) {
	t.Setenv("PI_PACKAGE_DIR", "/pkg") // make ReadmePath/DocsPath/ExamplesPath deterministic
	cwd := t.TempDir()

	got := ResourceLoader{CWD: cwd}.BuildSystemPrompt(cli.Args{}, toolInfo("read", "bash", "edit", "write"))

	body := "You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.\n\n" +
		"Available tools:\n" +
		"- read: Read file contents\n" +
		"- bash: Execute bash commands (ls, grep, find, etc.)\n" +
		"- edit: Make precise file edits with exact text replacement, including multiple disjoint edits in one call\n" +
		"- write: Create or overwrite files\n\n" +
		"In addition to the tools above, you may have access to other custom tools depending on the project.\n\n" +
		"Guidelines:\n" +
		"- Use bash for file operations like ls, rg, find\n" +
		"- Use read to examine files instead of cat or sed.\n" +
		"- Use edit for precise changes (edits[].oldText must match exactly)\n" +
		"- When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls\n" +
		"- Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.\n" +
		"- Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.\n" +
		"- Use write only for new files or complete rewrites.\n" +
		"- Be concise in your responses\n" +
		"- Show file paths clearly when working with files\n\n" +
		"Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):\n" +
		"- Main documentation: /pkg/README.md\n" +
		"- Additional docs: /pkg/docs\n" +
		"- Examples: /pkg/examples (extensions, custom tools, SDK)\n" +
		"- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n" +
		"- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md)\n" +
		"- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing\n" +
		"- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)"

	want := body +
		"\nCurrent date: " + time.Now().Format("2006-01-02") +
		"\nCurrent working directory: " + filepath.ToSlash(cwd)

	if got != want {
		t.Fatalf("default prompt byte-shape mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuildSystemPromptNoToolsShowsNoneAndAlwaysOnGuidelines verifies the
// "(none)" tool list and that only the two always-on guidelines appear when no
// tools are present (no bash-only rule, no per-tool guidelines).
func TestBuildSystemPromptNoToolsShowsNoneAndAlwaysOnGuidelines(t *testing.T) {
	got := ResourceLoader{CWD: t.TempDir()}.BuildSystemPrompt(cli.Args{}, ToolPromptInfo{})
	if !strings.Contains(got, "Available tools:\n(none)\n") {
		t.Fatalf("expected (none) tool list:\n%s", got)
	}
	want := "Guidelines:\n- Be concise in your responses\n- Show file paths clearly when working with files\n\n"
	if !strings.Contains(got, want) {
		t.Fatalf("expected only the two always-on guidelines:\n%s", got)
	}
}

// TestBuildSystemPromptCustomSkipsToolSections verifies a custom prompt replaces
// the Available tools / Guidelines / Pi documentation sections entirely (TS
// system-prompt.ts:53-81).
func TestBuildSystemPromptCustomSkipsToolSections(t *testing.T) {
	got := ResourceLoader{CWD: t.TempDir()}.BuildSystemPrompt(cli.Args{SystemPrompt: "MY CUSTOM"}, toolInfo("read", "bash", "edit", "write"))
	if !strings.Contains(got, "MY CUSTOM") {
		t.Fatalf("custom prompt missing custom text:\n%s", got)
	}
	for _, banned := range []string{"Available tools:", "Guidelines:", "Pi documentation"} {
		if strings.Contains(got, banned) {
			t.Fatalf("custom prompt must not contain %q:\n%s", banned, got)
		}
	}
}

// TestBuildSystemPromptVisibleToolsOnlyWithSnippet verifies a tool without a
// snippet is omitted from the Available tools list (TS system-prompt.ts:91).
func TestBuildSystemPromptVisibleToolsOnlyWithSnippet(t *testing.T) {
	info := ToolPromptInfo{
		OrderedNames: []string{"read", "mystery"},
		Snippets:     map[string]string{"read": "Read file contents"},
	}
	got := ResourceLoader{CWD: t.TempDir()}.BuildSystemPrompt(cli.Args{}, info)
	if !strings.Contains(got, "- read: Read file contents") {
		t.Fatalf("expected read tool line:\n%s", got)
	}
	if strings.Contains(got, "mystery") {
		t.Fatalf("tool without a snippet must not appear in Available tools:\n%s", got)
	}
}

// TestBuildSystemPromptDedupsGuidelines verifies guideline dedup: a per-tool
// guideline equal to an always-on bullet appears once, and repeated custom
// guidelines collapse (TS system-prompt.ts:98-104 addGuideline set).
func TestBuildSystemPromptDedupsGuidelines(t *testing.T) {
	info := ToolPromptInfo{
		OrderedNames: []string{"read"},
		Snippets:     map[string]string{"read": "Read file contents"},
		Guidelines:   []string{"Be concise in your responses", "Custom rule", "Custom rule"},
	}
	got := ResourceLoader{CWD: t.TempDir()}.BuildSystemPrompt(cli.Args{}, info)
	if n := strings.Count(got, "- Be concise in your responses"); n != 1 {
		t.Fatalf("expected exactly 1 'Be concise' bullet, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "- Custom rule"); n != 1 {
		t.Fatalf("expected exactly 1 'Custom rule' bullet, got %d:\n%s", n, got)
	}
}
