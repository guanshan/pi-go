package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

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

	prompt := loader.BuildSystemPrompt(cli.Args{}, []string{"read: read a file"})
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

	withRead := loader.BuildSystemPrompt(cli.Args{}, []string{"grep: search", "read: read a file"})
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

	withoutRead := loader.BuildSystemPrompt(cli.Args{}, []string{"grep: search"})
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
	prompt := loader.BuildSystemPrompt(cli.Args{}, []string{"read: read a file"})
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

	prompt := loader.BuildSystemPrompt(cli.Args{}, []string{"read: read a file"})
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
	suppressed := loader.BuildSystemPrompt(cli.Args{NoContextFiles: true}, []string{"read: read a file"})
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
	def := ResourceLoader{CWD: cwd}.BuildSystemPrompt(cli.Args{}, []string{"read: read a file"})
	if !strings.Contains(def, wantDate) || !strings.Contains(def, wantCwd) {
		t.Fatalf("default prompt missing date/cwd: %q", def)
	}
	if !strings.HasSuffix(strings.TrimSpace(def), wantCwd) {
		t.Fatalf("default prompt should end with cwd line: %q", def)
	}

	// Custom prompt (provided via args).
	custom := ResourceLoader{CWD: cwd}.BuildSystemPrompt(cli.Args{SystemPrompt: "MY CUSTOM PROMPT"}, []string{"read: read a file"})
	if !strings.Contains(custom, "MY CUSTOM PROMPT") {
		t.Fatalf("custom prompt missing custom text: %q", custom)
	}
	if !strings.Contains(custom, wantDate) || !strings.Contains(custom, wantCwd) {
		t.Fatalf("custom prompt missing date/cwd: %q", custom)
	}
}
