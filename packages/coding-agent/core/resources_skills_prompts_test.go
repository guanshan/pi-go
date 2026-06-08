package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

// --- P1-11: prompt-template argument substitution -------------------------

func TestParseCommandArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a b c", []string{"a", "b", "c"}},
		{"  a   b  ", []string{"a", "b"}},
		{`"hello world" foo`, []string{"hello world", "foo"}},
		{`'single quoted' bar`, []string{"single quoted", "bar"}},
		{`mix"ed quote"s`, []string{"mixed quotes"}},
		{`""`, nil}, // TS only pushes non-empty current tokens
		{`"" second`, []string{"second"}},
	}
	for _, c := range cases {
		got := parseCommandArgs(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("parseCommandArgs(%q) = %#v, want %#v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("parseCommandArgs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSubstituteArgs(t *testing.T) {
	args := []string{"main.go", "src", "lib", "x"}
	cases := []struct {
		body string
		want string
	}{
		{"review $1", "review main.go"},
		{"order $2 then $1", "order src then main.go"},
		{"missing $9 here", "missing  here"},                    // out-of-range -> ""
		{"all: $@", "all: main.go src lib x"},                   // $@
		{"all: $ARGUMENTS", "all: main.go src lib x"},           // $ARGUMENTS
		{"from2: ${@:2}", "from2: src lib x"},                   // slice from N
		{"two from2: ${@:2:2}", "two from2: src lib"},           // slice N length L
		{"zero start: ${@:0}", "zero start: main.go src lib x"}, // bash: 0 treated as 1
		{"len over: ${@:3:9}", "len over: lib x"},               // length past end clamps
		{"start over: ${@:9}", "start over: "},                  // start past end -> empty
	}
	for _, c := range cases {
		if got := substituteArgs(c.body, args); got != c.want {
			t.Fatalf("substituteArgs(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

// TestSubstituteArgsNoRecursiveExpansion verifies an argument value containing a
// placeholder is not itself re-substituted (TS substituteArgs ordering note).
func TestSubstituteArgsNoRecursiveExpansion(t *testing.T) {
	if got := substituteArgs("v=$1 all=$@", []string{"$2", "second"}); got != "v=$2 all=$2 second" {
		t.Fatalf("recursive substitution leaked: %q", got)
	}
}

// TestExpandInputPromptTemplateWithArgs verifies the whole ExpandInput path:
// "/name args" must match (even with whitespace) and substitute placeholders,
// closing P1-11's primary defect.
func TestExpandInputPromptTemplateWithArgs(t *testing.T) {
	loader := ResourceLoader{
		PromptTemplates: map[string]PromptTemplate{
			"review": {Name: "review", Content: "Please review $1 carefully. Args: $@"},
		},
	}

	got, ok := loader.ExpandInput("/review main.go extra")
	if !ok {
		t.Fatalf("ExpandInput did not match /review with args")
	}
	if want := "Please review main.go carefully. Args: main.go extra"; got != want {
		t.Fatalf("ExpandInput = %q, want %q", got, want)
	}

	// Bare invocation with no args still substitutes (placeholders -> "").
	bare, ok := loader.ExpandInput("/review")
	if !ok {
		t.Fatalf("ExpandInput did not match bare /review")
	}
	if want := "Please review  carefully. Args: "; bare != want {
		t.Fatalf("bare ExpandInput = %q, want %q", bare, want)
	}

	// Unknown template falls through unchanged.
	if out, ok := loader.ExpandInput("/nope arg"); ok || out != "/nope arg" {
		t.Fatalf("unknown template should not expand: %q ok=%v", out, ok)
	}
}

// TestAddPromptTemplateFrontmatter verifies description + argument-hint are read
// from frontmatter, with the first-line fallback when description is absent
// (P1-11 / TS loadTemplateFromFile).
func TestAddPromptTemplateFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.md")
	writeFileT(t, path, "---\ndescription: Deploy the app\nargument-hint: \"[env]\"\n---\nBody using $1")

	loader := ResourceLoader{PromptTemplates: map[string]PromptTemplate{}}
	loader.addPromptTemplate(path, ResourceSourceInfo{})

	tmpl := loader.PromptTemplates["deploy"]
	if tmpl.Description != "Deploy the app" {
		t.Fatalf("description = %q, want %q", tmpl.Description, "Deploy the app")
	}
	if tmpl.ArgumentHint != "[env]" {
		t.Fatalf("argumentHint = %q, want %q", tmpl.ArgumentHint, "[env]")
	}
	if tmpl.Content != "Body using $1" {
		t.Fatalf("content = %q, want body without frontmatter", tmpl.Content)
	}

	// No-frontmatter-description: falls back to first non-empty body line.
	path2 := filepath.Join(dir, "plain.md")
	writeFileT(t, path2, "\nFirst meaningful line\nsecond")
	loader.addPromptTemplate(path2, ResourceSourceInfo{})
	if got := loader.PromptTemplates["plain"].Description; got != "First meaningful line" {
		t.Fatalf("fallback description = %q", got)
	}
}

// --- P1-12: skill frontmatter (name/description/disable-model-invocation) ---

// TestAddSkillFrontmatterName verifies the skill name comes from frontmatter,
// falling back to the parent directory name (P1-12).
func TestAddSkillFrontmatterName(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "tools")
	path := filepath.Join(skillDir, "SKILL.md")
	writeFileT(t, path, "---\nname: deploy\ndescription: Deploy stuff\n---\nbody")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(skillDir, path, ResourceSourceInfo{})

	if _, ok := loader.Skills["deploy"]; !ok {
		t.Fatalf("skill should be keyed by frontmatter name 'deploy': %#v", loader.Skills)
	}
	if _, ok := loader.Skills["tools"]; ok {
		t.Fatalf("skill must not be keyed by parent dir when frontmatter name present")
	}

	// Fallback to parent dir name when no frontmatter name.
	skillDir2 := filepath.Join(dir, "review")
	path2 := filepath.Join(skillDir2, "SKILL.md")
	writeFileT(t, path2, "---\ndescription: Review code\n---\nbody")
	loader.addSkill(skillDir2, path2, ResourceSourceInfo{})
	if loader.Skills["review"].Description != "Review code" {
		t.Fatalf("fallback name skill missing/wrong: %#v", loader.Skills)
	}
}

// TestAddSkillRequiresDescription verifies a skill with no frontmatter
// description is rejected with a diagnostic (P1-12 / TS loadSkillFromFile).
func TestAddSkillRequiresDescription(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "nodesc")
	path := filepath.Join(skillDir, "SKILL.md")
	writeFileT(t, path, "no frontmatter here, just body")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(skillDir, path, ResourceSourceInfo{})

	if len(loader.Skills) != 0 {
		t.Fatalf("skill without description must not load: %#v", loader.Skills)
	}
	if !hasDiagnostic(loader.Diagnostics, "description is required") {
		t.Fatalf("expected 'description is required' diagnostic: %#v", loader.Diagnostics)
	}
}

// TestSkillDisableModelInvocation verifies disable-model-invocation skills are
// excluded from the <available_skills> prompt block but remain invokable via
// /skill:name (P1-12 / TS formatSkillsForPrompt).
func TestSkillDisableModelInvocation(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, "hidden")
	hiddenPath := filepath.Join(hiddenDir, "SKILL.md")
	writeFileT(t, hiddenPath, "---\nname: hidden\ndescription: A hidden skill\ndisable-model-invocation: true\n---\nhidden body")
	shownDir := filepath.Join(dir, "shown")
	shownPath := filepath.Join(shownDir, "SKILL.md")
	writeFileT(t, shownPath, "---\nname: shown\ndescription: A shown skill\n---\nshown body")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(hiddenDir, hiddenPath, ResourceSourceInfo{})
	loader.addSkill(shownDir, shownPath, ResourceSourceInfo{})

	if !loader.Skills["hidden"].DisableModelInvocation {
		t.Fatalf("hidden skill should have DisableModelInvocation=true")
	}

	prompt := loader.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	if strings.Contains(prompt, "<name>hidden</name>") {
		t.Fatalf("disable-model-invocation skill must not appear in prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<name>shown</name>") {
		t.Fatalf("visible skill missing from prompt:\n%s", prompt)
	}

	// Still invokable explicitly via /skill:name.
	expanded, ok := loader.ExpandInput("/skill:hidden")
	if !ok || !strings.Contains(expanded, "hidden body") {
		t.Fatalf("/skill:hidden should still expand: %q ok=%v", expanded, ok)
	}
}

// TestSkillCommandExpansionStripsFrontmatterAndAddsReferenceBase verifies explicit
// /skill:name expansion mirrors TS _expandSkillCommand: frontmatter is stripped,
// the body is trimmed, and the relative-reference base directory is included.
func TestSkillCommandExpansionStripsFrontmatterAndAddsReferenceBase(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "review")
	path := filepath.Join(skillDir, "SKILL.md")
	writeFileT(t, path, "---\nname: review\ndescription: Review code\n---\n\nBody line\n")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(skillDir, path, ResourceSourceInfo{})

	expanded, ok := loader.ExpandInput("/skill:review check this")
	if !ok {
		t.Fatal("/skill:review did not expand")
	}
	if strings.Contains(expanded, "---") || strings.Contains(expanded, "description:") {
		t.Fatalf("skill expansion leaked frontmatter:\n%s", expanded)
	}
	if !strings.Contains(expanded, "References are relative to "+skillDir+".") {
		t.Fatalf("skill expansion missing reference base:\n%s", expanded)
	}
	if !strings.Contains(expanded, "\n\nBody line\n</skill>\n\ncheck this") {
		t.Fatalf("skill expansion body/user message mismatch:\n%s", expanded)
	}
}

// --- P2-21: discovery (recursion, ignore files, validation, collisions) -----

// TestLoadSkillsDeepRecursion verifies a directory is scanned to arbitrary depth
// for SKILL.md, and loose .md children at the scan root are loaded (P2-21).
func TestLoadSkillsDeepRecursion(t *testing.T) {
	root := t.TempDir()
	// Deeply nested skill.
	deep := filepath.Join(root, "a", "b", "c", "deepskill")
	writeFileT(t, filepath.Join(deep, "SKILL.md"), "---\nname: deepskill\ndescription: A deep skill\n---\nbody")
	// Loose .md at the scan root.
	writeFileT(t, filepath.Join(root, "loose"), "ignored: this is a dir marker") // not a skill
	writeFileT(t, filepath.Join(root, "loose.md"), "---\nname: loose\ndescription: A loose skill\n---\nbody")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.loadSkills(root, ResourceSourceInfo{})

	if _, ok := loader.Skills["deepskill"]; !ok {
		t.Fatalf("deeply-nested skill not discovered: %#v", keys(loader.Skills))
	}
	if _, ok := loader.Skills["loose"]; !ok {
		t.Fatalf("loose root .md not loaded as skill: %#v", keys(loader.Skills))
	}
}

// TestLoadSkillsStopsAtSkillMd verifies that once a directory has SKILL.md it is
// treated as a skill root and not recursed into (P2-21 / TS rule).
func TestLoadSkillsStopsAtSkillMd(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "parent")
	writeFileT(t, filepath.Join(skillRoot, "SKILL.md"), "---\nname: parent\ndescription: Parent skill\n---\nbody")
	// A nested SKILL.md that must NOT be discovered because recursion stops.
	writeFileT(t, filepath.Join(skillRoot, "nested", "SKILL.md"), "---\nname: nested\ndescription: Nested skill\n---\nbody")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.loadSkills(root, ResourceSourceInfo{})

	if _, ok := loader.Skills["parent"]; !ok {
		t.Fatalf("parent skill not loaded: %#v", keys(loader.Skills))
	}
	if _, ok := loader.Skills["nested"]; ok {
		t.Fatalf("recursion must stop at SKILL.md; nested should not load: %#v", keys(loader.Skills))
	}
}

// TestLoadSkillsSkipsDotfilesAndNodeModules verifies dotfile dirs and
// node_modules are skipped during discovery (P2-21).
func TestLoadSkillsSkipsDotfilesAndNodeModules(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, ".hidden", "x", "SKILL.md"), "---\nname: hidden\ndescription: d\n---\nb")
	writeFileT(t, filepath.Join(root, "node_modules", "pkg", "SKILL.md"), "---\nname: pkg\ndescription: d\n---\nb")
	writeFileT(t, filepath.Join(root, "real", "SKILL.md"), "---\nname: real\ndescription: d\n---\nb")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.loadSkills(root, ResourceSourceInfo{})

	if _, ok := loader.Skills["real"]; !ok {
		t.Fatalf("real skill missing: %#v", keys(loader.Skills))
	}
	if _, ok := loader.Skills["hidden"]; ok {
		t.Fatalf("dotfile dir must be skipped")
	}
	if _, ok := loader.Skills["pkg"]; ok {
		t.Fatalf("node_modules must be skipped")
	}
}

// TestLoadSkillsHonorsIgnoreFiles verifies .gitignore/.ignore/.fdignore exclude
// matching skill subtrees (P2-21).
func TestLoadSkillsHonorsIgnoreFiles(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	writeFileT(t, filepath.Join(root, "ignored", "SKILL.md"), "---\nname: ignored\ndescription: d\n---\nb")
	writeFileT(t, filepath.Join(root, "kept", "SKILL.md"), "---\nname: kept\ndescription: d\n---\nb")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.loadSkills(root, ResourceSourceInfo{})

	if _, ok := loader.Skills["kept"]; !ok {
		t.Fatalf("kept skill missing: %#v", keys(loader.Skills))
	}
	if _, ok := loader.Skills["ignored"]; ok {
		t.Fatalf(".gitignore'd skill must be skipped: %#v", keys(loader.Skills))
	}
}

// TestSkillNameAndDescriptionValidation verifies name/description validation
// emits diagnostics; an invalid-name skill with a valid description still loads
// (with a warning), per TS (P2-21).
func TestSkillNameAndDescriptionValidation(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "Bad_Name")
	path := filepath.Join(skillDir, "SKILL.md")
	// Uppercase + underscore name -> invalid charset; valid description.
	writeFileT(t, path, "---\nname: Bad_Name\ndescription: still valid\n---\nbody")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(skillDir, path, ResourceSourceInfo{})

	if _, ok := loader.Skills["Bad_Name"]; !ok {
		t.Fatalf("skill with valid description should load despite name warning: %#v", keys(loader.Skills))
	}
	if !hasDiagnostic(loader.Diagnostics, "name contains invalid characters") {
		t.Fatalf("expected invalid-name diagnostic: %#v", loader.Diagnostics)
	}
}

// TestSkillNameCollisionKeepsFirst verifies a name collision keeps the first
// skill and emits a collision diagnostic (P2-21).
func TestSkillNameCollisionKeepsFirst(t *testing.T) {
	dir := t.TempDir()
	firstDir := filepath.Join(dir, "first")
	first := filepath.Join(firstDir, "SKILL.md")
	writeFileT(t, first, "---\nname: dup\ndescription: first wins\n---\nfirst")
	secondDir := filepath.Join(dir, "second")
	second := filepath.Join(secondDir, "SKILL.md")
	writeFileT(t, second, "---\nname: dup\ndescription: second loses\n---\nsecond")

	loader := ResourceLoader{Skills: map[string]Skill{}, skillRealPaths: map[string]bool{}}
	loader.addSkill(firstDir, first, ResourceSourceInfo{})
	loader.addSkill(secondDir, second, ResourceSourceInfo{})

	if loader.Skills["dup"].Description != "first wins" {
		t.Fatalf("collision should keep FIRST skill: %#v", loader.Skills["dup"])
	}
	found := false
	for _, d := range loader.Diagnostics {
		if d.Type == "collision" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a collision diagnostic: %#v", loader.Diagnostics)
	}
}

// --- P3-23: system-prompt skill block emitted in load order ----------------

// TestRenderSkillsPreservesLoadOrder verifies the <available_skills> block emits
// skills in discovery/load order (SkillOrder), not alphabetical (P3-23).
func TestRenderSkillsPreservesLoadOrder(t *testing.T) {
	loader := ResourceLoader{
		Skills: map[string]Skill{
			"zebra": {Name: "zebra", Path: "/z", Description: "z"},
			"alpha": {Name: "alpha", Path: "/a", Description: "a"},
			"mid":   {Name: "mid", Path: "/m", Description: "m"},
		},
		// Discovery order: zebra, then alpha, then mid (NOT alphabetical).
		SkillOrder: []string{"zebra", "alpha", "mid"},
	}

	prompt := loader.BuildSystemPrompt(cli.Args{}, toolInfo("read"))
	zi := strings.Index(prompt, "<name>zebra</name>")
	ai := strings.Index(prompt, "<name>alpha</name>")
	mi := strings.Index(prompt, "<name>mid</name>")
	if zi < 0 || ai < 0 || mi < 0 {
		t.Fatalf("missing a skill in prompt:\n%s", prompt)
	}
	if zi >= ai || ai >= mi {
		t.Fatalf("skills not in load order (zebra<alpha<mid): zebra=%d alpha=%d mid=%d", zi, ai, mi)
	}
}

// TestLoadResourcesSkillOrderUserBeforeProject verifies cross-source ordering:
// user-dir skills precede project-dir skills in SkillOrder (P3-23).
func TestLoadResourcesSkillOrderUserBeforeProject(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	writeFileT(t, filepath.Join(agentDir, "skills", "userskill", "SKILL.md"), "---\nname: userskill\ndescription: u\n---\nb")
	writeFileT(t, filepath.Join(ProjectPiDir(cwd), "skills", "projskill", "SKILL.md"), "---\nname: projskill\ndescription: p\n---\nb")

	loader := LoadResources(cwd, agentDir, cli.Args{
		NoContextFiles: true, NoPromptTemplates: true, NoThemes: true, NoExtensions: true,
	}, nil)

	ui := indexOf(loader.SkillOrder, "userskill")
	pi := indexOf(loader.SkillOrder, "projskill")
	if ui < 0 || pi < 0 {
		t.Fatalf("expected both skills in order, got %#v", loader.SkillOrder)
	}
	if ui >= pi {
		t.Fatalf("user skill should precede project skill: order=%#v", loader.SkillOrder)
	}
}

// --- helpers ---------------------------------------------------------------

func hasDiagnostic(diags []cli.Diagnostic, substr string) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func keys(m map[string]Skill) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
