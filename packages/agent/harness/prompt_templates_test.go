package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
)

func TestLoadPromptTemplatesNonRecursiveExplicitAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessTestFile(t, filepath.Join(root, "a", "one.md"), "---\ndescription: One template\n---\nHello $1")
	writeHarnessTestFile(t, filepath.Join(root, "a", "nested", "ignored.md"), "Ignored")
	writeHarnessTestFile(t, filepath.Join(root, "a", "skip.txt"), "Ignored")
	writeHarnessTestFile(t, filepath.Join(root, "b", "two.md"), "First line description\nBody")
	writeHarnessTestFile(t, filepath.Join(root, "broken.md"), "---\ndescription: [unterminated\n---\nBody")
	if err := os.Symlink(filepath.Join(root, "b", "two.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("requires symlink support: %v", err)
	}

	loaded := LoadPromptTemplates(ctx, env, "a", "b", "broken.md", "link.md", "missing.txt", "a/skip.txt")

	if len(loaded.PromptTemplates) != 3 {
		t.Fatalf("templates=%#v diagnostics=%#v", loaded.PromptTemplates, loaded.Diagnostics)
	}
	if loaded.PromptTemplates[0] != (PromptTemplate{Name: "one", Description: "One template", Content: "Hello $1"}) {
		t.Fatalf("first template=%#v", loaded.PromptTemplates[0])
	}
	if loaded.PromptTemplates[1].Name != "two" || loaded.PromptTemplates[1].Description != "First line description" {
		t.Fatalf("second template=%#v", loaded.PromptTemplates[1])
	}
	if loaded.PromptTemplates[2].Name != "link" || loaded.PromptTemplates[2].Content != "First line description\nBody" {
		t.Fatalf("link template=%#v", loaded.PromptTemplates[2])
	}
	if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Code != "parse_failed" {
		t.Fatalf("diagnostics=%#v", loaded.Diagnostics)
	}
}

func TestLoadSourcedPromptTemplatesPreservesDuplicatesAndSources(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	writeHarnessTestFile(t, filepath.Join(root, "user", "demo.md"), "---\ndescription: User\n---\nuser")
	writeHarnessTestFile(t, filepath.Join(root, "project", "demo.md"), "---\ndescription: Project\n---\nproject")
	writeHarnessTestFile(t, filepath.Join(root, "project", "broken.md"), "---\ndescription: [bad\n---\nbad")

	loaded := LoadSourcedPromptTemplates(ctx, env, []SourcedPromptTemplateInput{
		{Path: "user", Source: "user"},
		{Path: "project", Source: "project"},
	})

	if len(loaded.PromptTemplates) != 2 || loaded.PromptTemplates[0].PromptTemplate.Content != "user" || loaded.PromptTemplates[1].PromptTemplate.Content != "project" {
		t.Fatalf("templates=%#v", loaded.PromptTemplates)
	}
	if loaded.PromptTemplates[0].Source != "user" || loaded.PromptTemplates[1].Source != "project" {
		t.Fatalf("sources=%#v", loaded.PromptTemplates)
	}
	if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Source != "project" {
		t.Fatalf("diagnostics=%#v", loaded.Diagnostics)
	}
}

func TestPromptTemplateArgumentSubstitution(t *testing.T) {
	tmpl := PromptTemplate{Name: "demo", Content: "$1 ${@:2} ${@:2:1} $ARGUMENTS $@ {1} {{1}}"}
	got := FormatPromptTemplateInvocation(tmpl, []string{"hello world", "test", "again"})
	want := "hello world test again test hello world test again hello world test again {1} {{1}}"
	if got != want {
		t.Fatalf("formatted=%q", got)
	}
	args := ParseCommandArgs(`one "two words" 'three words' escaped\ space`)
	wantArgs := []string{"one", "two words", "three words", `escaped\`, "space"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args=%#v", args)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args=%#v", args)
		}
	}
}

// P2-03: ParseCommandArgs edge cases must match TS parseCommandArgs exactly.
func TestParseCommandArgsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		// Bare empty quotes emit no argument (TS pushes only truthy current).
		{"empty double quotes", `""`, nil},
		{"empty single quotes", `''`, nil},
		{"empty quotes between args", `a "" b`, []string{"a", "b"}},
		// Adjacent quotes concatenate into the surrounding token.
		{"empty quotes inside token", `a""b`, []string{"ab"}},
		// Only space and tab separate; other Unicode whitespace is literal.
		{"tab separates", "a\tb", []string{"a", "b"}},
		{"newline is literal", "a\nb", []string{"a\nb"}},
		{"nbsp is literal", "a b", []string{"a b"}},
		// Input is not trimmed, but leading/trailing spaces just yield no empty args.
		{"surrounding spaces", "  a  ", []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCommandArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseCommandArgs(%q)=%#v want %#v", tc.in, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("ParseCommandArgs(%q)=%#v want %#v", tc.in, got, tc.want)
				}
			}
		})
	}
}

// P2-03: ${@:N} with a negative/non-numeric N is left literal (the TS regex only
// matches \d+); valid forms clamp to the argument bounds.
func TestSubstituteArgSlicesEdgeCases(t *testing.T) {
	args := []string{"a", "b", "c"}
	cases := []struct {
		in   string
		want string
	}{
		{"${@:-1}", "${@:-1}"},     // negative start: left literal
		{"${@:-1:2}", "${@:-1:2}"}, // negative start with length: literal
		{"${@:x}", "${@:x}"},       // non-numeric: literal
		{"${@:0}", "a b c"},        // start 0 -> index -1 clamped to 0
		{"${@:1}", "a b c"},        // 1-based: from first arg
		{"${@:2}", "b c"},          // from second arg
		{"${@:2:1}", "b"},          // length-limited
		{"${@:2:99}", "b c"},       // length beyond end clamps
		{"${@:99}", ""},            // start beyond end -> empty
	}
	for _, tc := range cases {
		if got := SubstitutePromptArgs(tc.in, args); got != tc.want {
			t.Fatalf("SubstitutePromptArgs(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEnvPathHelpersHandleWindowsSeparators(t *testing.T) {
	root := `C:\work\project\skills`
	file := `C:\work\project\skills\inspect\SKILL.md`
	if got := baseEnvPath(file); got != "SKILL.md" {
		t.Fatalf("base=%q", got)
	}
	if got := dirEnvPath(file); got != "C:/work/project/skills/inspect" {
		t.Fatalf("dir=%q", got)
	}
	if got := relativeEnvPath(root, file); got != "inspect/SKILL.md" {
		t.Fatalf("relative=%q", got)
	}
	if got := joinEnvPath(root, `inspect\SKILL.md`); got != "C:/work/project/skills/inspect/SKILL.md" {
		t.Fatalf("join=%q", got)
	}
}

func writeHarnessTestFile(t *testing.T, filePath string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
