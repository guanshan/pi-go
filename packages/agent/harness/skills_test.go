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
		t.Fatal(err)
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
