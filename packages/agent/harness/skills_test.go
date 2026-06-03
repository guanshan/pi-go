package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
)

func TestLoadSkillsUsesExecutionEnvAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessTestFile(t, filepath.Join(root, ".agents", "skills", "example", "SKILL.md"), `---
name: example
description: Example skill
disable-model-invocation: true
---
Use this skill.
`)
	writeHarnessTestFile(t, filepath.Join(root, ".agents", "skills", "broken", "SKILL.md"), `---
name: broken
---
Missing description.`)

	loaded := LoadSkills(ctx, env, ".agents/skills", "missing")

	if len(loaded.Skills) != 1 {
		t.Fatalf("skills=%#v diagnostics=%#v", loaded.Skills, loaded.Diagnostics)
	}
	if got := loaded.Skills[0]; got.Name != "example" || got.Description != "Example skill" || got.Content != "Use this skill." || !got.DisableModelInvocation {
		t.Fatalf("skill=%#v", got)
	}
	if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Code != "invalid_metadata" || loaded.Diagnostics[0].Message != "description is required" {
		t.Fatalf("diagnostics=%#v", loaded.Diagnostics)
	}
}

func TestLoadSkillsRecursesHonorsIgnoresRootMarkdownAndSymlinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessTestFile(t, filepath.Join(root, "skills", "root.md"), "---\ndescription: Root skill\n---\nRoot content")
	writeHarnessTestFile(t, filepath.Join(root, "skills", "nested", "ignored.md"), "---\ndescription: Ignored\n---\nIgnored")
	writeHarnessTestFile(t, filepath.Join(root, "recursive", "keep", "SKILL.md"), "---\nname: keep\ndescription: Keep skill\n---\nKeep")
	writeHarnessTestFile(t, filepath.Join(root, "recursive", "skip", "SKILL.md"), "---\nname: skip\ndescription: Skip skill\n---\nSkip")
	writeHarnessTestFile(t, filepath.Join(root, "recursive", ".gitignore"), "skip/\n")
	if err := os.Symlink(filepath.Join(root, "recursive"), filepath.Join(root, "skills-link")); err != nil {
		t.Skipf("requires symlink support: %v", err)
	}

	rootLoaded := LoadSkills(ctx, env, "skills")
	if len(rootLoaded.Skills) != 1 || rootLoaded.Skills[0].Name != "skills" || rootLoaded.Skills[0].Content != "Root content" {
		t.Fatalf("root skills=%#v", rootLoaded.Skills)
	}
	recursiveLoaded := LoadSkills(ctx, env, "skills-link")
	if len(recursiveLoaded.Skills) != 1 || recursiveLoaded.Skills[0].Name != "keep" {
		t.Fatalf("recursive skills=%#v diagnostics=%#v", recursiveLoaded.Skills, recursiveLoaded.Diagnostics)
	}
	if !strings.Contains(recursiveLoaded.Skills[0].FilePath, "skills-link/keep/SKILL.md") {
		t.Fatalf("symlink file path=%q", recursiveLoaded.Skills[0].FilePath)
	}
}

func TestLoadSourcedSkillsPreservesDuplicatesSourcesAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessTestFile(t, filepath.Join(root, "user", "demo", "SKILL.md"), "---\nname: demo\ndescription: User\n---\nuser")
	writeHarnessTestFile(t, filepath.Join(root, "project", "demo", "SKILL.md"), "---\nname: demo\ndescription: Project\n---\nproject")
	writeHarnessTestFile(t, filepath.Join(root, "project", "bad", "SKILL.md"), "---\nname: bad\n---\nbad")

	loaded := LoadSourcedSkills(ctx, env, []SourcedSkillInput{
		{Path: "user", Source: "user"},
		{Path: "project", Source: "project"},
	})

	if len(loaded.Skills) != 2 || loaded.Skills[0].Skill.Content != "user" || loaded.Skills[1].Skill.Content != "project" {
		t.Fatalf("sourced skills=%#v", loaded.Skills)
	}
	if loaded.Skills[0].Source != "user" || loaded.Skills[1].Source != "project" {
		t.Fatalf("sources=%#v", loaded.Skills)
	}
	if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Source != "project" {
		t.Fatalf("diagnostics=%#v", loaded.Diagnostics)
	}
}

// TestLoadSkillsEmitsInvalidNameYamlAndLongNameDiagnostics converts the
// diagnostics cases from coding-agent/test/skills.test.ts:57-161 (invalid name
// characters, name >64 chars, consecutive hyphens, invalid YAML frontmatter).
// The TS assertions use diagnostics.some(d => d.message.includes(...)), so we
// mirror that with substring checks rather than exact equality.
func TestLoadSkillsEmitsInvalidNameYamlAndLongNameDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Inline fixtures mirror coding-agent/test/fixtures/skills/* exactly: the
	// SKILL.md parent directory name matches the TS fixture directory name and
	// the frontmatter `name` matches the TS fixture frontmatter.
	writeHarnessTestFile(t, filepath.Join(root, "skills", "invalid-name-chars", "SKILL.md"), `---
name: Invalid_Name
description: A skill with invalid characters in the name.
---

# Invalid Name

This skill has uppercase and underscore in the name.
`)
	writeHarnessTestFile(t, filepath.Join(root, "skills", "long-name", "SKILL.md"), `---
name: this-is-a-very-long-skill-name-that-exceeds-the-sixty-four-character-limit-set-by-the-standard
description: A skill with a name that exceeds 64 characters.
---

# Long Name

This skill's name is too long.
`)
	writeHarnessTestFile(t, filepath.Join(root, "skills", "consecutive-hyphens", "SKILL.md"), `---
name: bad--name
description: A skill with consecutive hyphens in the name.
---

# Consecutive Hyphens

This skill has consecutive hyphens in its name.
`)
	writeHarnessTestFile(t, filepath.Join(root, "skills", "invalid-yaml", "SKILL.md"), `---
name: invalid-yaml
description: [unclosed bracket
---

# Invalid YAML Skill

This skill has invalid YAML in the frontmatter.
`)

	loaded := LoadSkills(ctx, env, "skills")

	// invalid-name-chars, long-name, consecutive-hyphens all have valid
	// descriptions, so the skill is still loaded (with warnings). invalid-yaml
	// fails to parse and is skipped, matching TS skills.length expectations.
	loadedNames := skillNames(loaded.Skills)
	for _, want := range []string{"Invalid_Name", "this-is-a-very-long-skill-name-that-exceeds-the-sixty-four-character-limit-set-by-the-standard", "bad--name"} {
		if !containsString(loadedNames, want) {
			t.Fatalf("expected skill %q to load; got skills=%v diagnostics=%#v", want, loadedNames, loaded.Diagnostics)
		}
	}
	if containsString(loadedNames, "invalid-yaml") {
		t.Fatalf("invalid-yaml skill should be skipped; got skills=%v", loadedNames)
	}

	// invalid characters (TS: "invalid characters", skills.test.ts:64)
	if !hasSkillDiagnostic(loaded.Diagnostics, "invalid_metadata", "invalid characters") {
		t.Fatalf("expected invalid-characters diagnostic; diagnostics=%#v", loaded.Diagnostics)
	}
	// name exceeds 64 characters (TS: "exceeds 64 characters", skills.test.ts:74)
	if !hasSkillDiagnostic(loaded.Diagnostics, "invalid_metadata", "exceeds 64 characters") {
		t.Fatalf("expected long-name diagnostic; diagnostics=%#v", loaded.Diagnostics)
	}
	// consecutive hyphens (TS: "consecutive hyphens", skills.test.ts:160)
	if !hasSkillDiagnostic(loaded.Diagnostics, "invalid_metadata", "consecutive hyphens") {
		t.Fatalf("expected consecutive-hyphens diagnostic; diagnostics=%#v", loaded.Diagnostics)
	}
	// invalid YAML frontmatter. TS asserts the message includes "at line"
	// (skills.test.ts:138); Go's gopkg.in/yaml.v3 surfaces the parse failure as
	// `parse_failed` with a "yaml: line N: ..." message, so we assert on the Go
	// shape (code parse_failed, message mentions a line). See
	// docs/TS_COMPATIBILITY.md for the message-format note.
	if !hasSkillDiagnostic(loaded.Diagnostics, "parse_failed", "line") {
		t.Fatalf("expected invalid-yaml parse_failed diagnostic; diagnostics=%#v", loaded.Diagnostics)
	}
}

// TestFormatSkillsForSystemPromptEscapesXML converts the XML-escape case from
// coding-agent/test/skills.test.ts:268 for FormatSkillsForSystemPrompt (the Go
// equivalent of system_prompt.go formatSkillsForPrompt / escapeXML).
func TestFormatSkillsForSystemPromptEscapesXML(t *testing.T) {
	skills := []Skill{
		{
			Name:        "test-skill",
			Description: `A skill with <special> & "characters".`,
			FilePath:    "/path/to/skill/SKILL.md",
		},
	}

	result := FormatSkillsForSystemPrompt(skills)

	if !strings.Contains(result, "&lt;special&gt;") {
		t.Fatalf("expected escaped angle brackets; result=%q", result)
	}
	if !strings.Contains(result, "&amp;") {
		t.Fatalf("expected escaped ampersand; result=%q", result)
	}
	if !strings.Contains(result, "&quot;characters&quot;") {
		t.Fatalf("expected escaped double quotes; result=%q", result)
	}
	// Guard against double-escaping: the raw characters must not survive in the
	// emitted description line.
	if strings.Contains(result, "<special>") || strings.Contains(result, `"characters"`) {
		t.Fatalf("raw special characters leaked into output; result=%q", result)
	}
}

func skillNames(skills []Skill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasSkillDiagnostic(diagnostics []SkillDiagnostic, code string, messageSubstring string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && strings.Contains(diagnostic.Message, messageSubstring) {
			return true
		}
	}
	return false
}

func TestFormatSkillInvocationMatchesTypeScript(t *testing.T) {
	skill := Skill{
		Name:        "inspect",
		Description: "Inspect things",
		Content:     "Use inspection tools.",
		FilePath:    "/project/.pi/skills/inspect/SKILL.md",
	}

	got := FormatSkillInvocation(skill, "Check errors.")
	want := "<skill name=\"inspect\" location=\"/project/.pi/skills/inspect/SKILL.md\">\nReferences are relative to /project/.pi/skills/inspect.\n\nUse inspection tools.\n</skill>\n\nCheck errors."
	if got != want {
		t.Fatalf("invocation=%q", got)
	}
}
