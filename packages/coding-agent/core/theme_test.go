package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestDefaultThemeNameClassifiesGrayscaleBackground(t *testing.T) {
	cases := []struct {
		colorFgBg string
		want      string
	}{
		{"15;0", "dark"},
		{"15;232", "dark"}, // dark grayscale (232=#080808) must stay dark
		{"15;243", "dark"}, // last dark gray
		{"0;244", "light"}, // light half of the grayscale ramp
		{"0;255", "light"},
		{"0;231", "light"}, // pure white cube corner
		{"0;15", "light"},
	}
	for _, tc := range cases {
		t.Setenv("COLORFGBG", tc.colorFgBg)
		if got := defaultThemeName(); got != tc.want {
			t.Errorf("COLORFGBG=%q: got %q, want %q", tc.colorFgBg, got, tc.want)
		}
	}
}

func TestResolveThemeSelectsConfiguredThemeAndResolvesVars(t *testing.T) {
	path := writeThemeFile(t, t.TempDir(), "berry.json", "berry", map[string]any{
		"accent": "#112233",
		"muted":  244,
	}, map[string]any{
		"brand": "#112233",
	})

	settings := &SettingsManager{Global: Settings{Theme: "berry"}}
	theme, diagnostics := ResolveTheme(settings, ResourceLoader{Themes: []string{path}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if theme.Name != "berry" || theme.SourcePath != path {
		t.Fatalf("theme=%#v", theme)
	}
	if theme.Color("accent") != "#112233" {
		t.Fatalf("accent=%q", theme.Color("accent"))
	}
	if theme.Color("muted") != "244" {
		t.Fatalf("muted=%q", theme.Color("muted"))
	}
}

func TestResolveThemeFallsBackOnInvalidTheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte(`{"name":"broken","colors":{"accent":"missing"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := &SettingsManager{Global: Settings{Theme: "broken"}}
	theme, diagnostics := ResolveTheme(settings, ResourceLoader{Themes: []string{path}})
	if theme.Name != "dark" {
		t.Fatalf("theme=%#v, want dark fallback", theme)
	}
	if !diagnosticsContain(diagnostics, "missing required color tokens") {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if !diagnosticsContain(diagnostics, "Theme not found: broken") {
		t.Fatalf("missing fallback diagnostic: %#v", diagnostics)
	}
}

func TestResolveThemeFirstResourceNameWins(t *testing.T) {
	dir := t.TempDir()
	first := writeThemeFile(t, dir, "a.json", "dupe", map[string]any{"accent": "#111111"}, nil)
	second := writeThemeFile(t, dir, "b.json", "dupe", map[string]any{"accent": "#222222"}, nil)

	settings := &SettingsManager{Global: Settings{Theme: "dupe"}}
	theme, diagnostics := ResolveTheme(settings, ResourceLoader{Themes: []string{first, second}})
	if theme.SourcePath != first || theme.Color("accent") != "#111111" {
		t.Fatalf("theme=%#v", theme)
	}
	if !diagnosticsContain(diagnostics, `theme name "dupe" collision`) {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
}

func TestResolveThemeBuiltInWinsOverResourceDuplicate(t *testing.T) {
	path := writeThemeFile(t, t.TempDir(), "dark.json", "dark", map[string]any{"accent": "#222222"}, nil)

	settings := &SettingsManager{Global: Settings{Theme: "dark"}}
	theme, diagnostics := ResolveTheme(settings, ResourceLoader{Themes: []string{path}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	if theme.SourcePath != "" || theme.Color("accent") != "#8abeb7" {
		t.Fatalf("theme=%#v, want built-in dark", theme)
	}
}

func TestCreateAgentSessionServicesResolvesSelectedTheme(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := writeThemeFile(t, t.TempDir(), "berry.json", "berry", map[string]any{"accent": "#112233"}, nil)
	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.Theme = "berry"

	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settings,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:       true,
			NoExtensions:         true,
			NoSkills:             true,
			NoPromptTemplates:    true,
			AdditionalThemePaths: []string{path},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if services.Theme.Name != "berry" || services.Theme.Color("accent") != "#112233" {
		t.Fatalf("theme=%#v", services.Theme)
	}

	registry := ai.NewModelRegistry(agentDir, ai.NewAuthStorage(agentDir))
	model, ok, _ := registry.Match("faux", "faux")
	if !ok {
		t.Fatal("missing faux model")
	}
	services.ModelRegistry = registry
	created, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
		Services: services,
		Model:    model,
		NoTools:  NoToolsAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session.Theme.Name != "berry" {
		t.Fatalf("session theme=%#v", created.Session.Theme)
	}
}

func TestNoThemesSkipsDiscoveredThemeApplication(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	themeDir := filepath.Join(agentDir, "themes")
	_ = writeThemeFile(t, themeDir, "berry.json", "berry", map[string]any{"accent": "#112233"}, nil)
	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.Theme = "berry"

	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settings,
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
	if services.Theme.Name != "dark" {
		t.Fatalf("theme=%#v, want dark fallback", services.Theme)
	}
	if !diagnosticsContain(services.Diagnostics, "Theme not found: berry") {
		t.Fatalf("diagnostics=%#v", services.Diagnostics)
	}
}

func TestCreateAgentSessionServicesResolvesPackageTheme(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageDir := filepath.Join(cwd, "theme-package")
	_ = writeThemeFile(t, filepath.Join(packageDir, "themes"), "berry.json", "berry", map[string]any{"accent": "#112233"}, nil)
	settings := NewSettingsManager(cwd, agentDir)
	settings.Global.Theme = "berry"
	settings.Global.Packages = []PackageSetting{{Source: packageDir}}

	services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: settings,
		ResourceLoaderOptions: DefaultResourceLoaderOptions{
			NoContextFiles:    true,
			NoExtensions:      true,
			NoSkills:          true,
			NoPromptTemplates: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if services.Theme.Name != "berry" || services.Theme.Color("accent") != "#112233" {
		t.Fatalf("theme=%#v", services.Theme)
	}
}

func TestInteractiveTUIUsesResolvedThemeStyles(t *testing.T) {
	runtime := testInteractiveRuntime(t)
	theme := resolvedTestTheme(t, map[string]any{
		"accent":          "#aabbcc",
		"selectedBg":      "#010203",
		"userMessageText": "#112233",
		"error":           "#445566",
		"mdHeading":       "#778899",
	})
	runtime.Session().Theme = theme

	model, err := newInteractiveModel(context.Background(), runtime, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(model.styles.User.GetForeground()); !strings.Contains(got, "17 34 51") {
		t.Fatalf("user foreground=%q", got)
	}
	if got := fmt.Sprint(model.styles.SelectorSelected.GetBackground()); !strings.Contains(got, "1 2 3") {
		t.Fatalf("selector selected background=%q", got)
	}
	model.appendMessage(interactiveRoleUser, "# heading")
	rendered := model.renderTranscript(80)
	if !strings.Contains(rendered, "heading") {
		t.Fatalf("rendered transcript=%q", rendered)
	}
}

func resolvedTestTheme(t *testing.T, overrides map[string]any) ResolvedTheme {
	t.Helper()
	path := writeThemeFile(t, t.TempDir(), "theme.json", "test-theme", overrides, nil)
	theme, err := loadResolvedThemeFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return theme
}

func writeThemeFile(t *testing.T, dir, name, themeName string, overrides, vars map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	colors := map[string]any{}
	for _, token := range themeRequiredColorTokens {
		colors[token] = "#101010"
	}
	for key, value := range overrides {
		colors[key] = value
	}
	if vars == nil {
		vars = map[string]any{}
	}
	if colors["accent"] == "#112233" {
		vars["brand"] = "#112233"
		colors["accent"] = "brand"
	}
	raw, err := json.Marshal(map[string]any{
		"name":   themeName,
		"vars":   vars,
		"colors": colors,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func diagnosticsContain(diagnostics []Diagnostic, needle string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, needle) {
			return true
		}
	}
	return false
}
