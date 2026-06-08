package core

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/guanshan/pi-go/packages/tui"
)

type ThemeConfig struct {
	Name       string
	SourcePath string
	Vars       map[string]themeRawValue
	Colors     map[string]themeRawValue
	Export     map[string]themeRawValue
}

type ResolvedTheme struct {
	Name       string
	SourcePath string
	Colors     map[string]string
	Export     map[string]string
}

type themeRawValue struct {
	stringValue string
	numberValue int
	isNumber    bool
}

var themeRequiredColorTokens = []string{
	"accent", "border", "borderAccent", "borderMuted", "success", "error", "warning", "muted", "dim", "text", "thinkingText",
	"selectedBg", "userMessageBg", "userMessageText", "customMessageBg", "customMessageText", "customMessageLabel",
	"toolPendingBg", "toolSuccessBg", "toolErrorBg", "toolTitle", "toolOutput",
	"mdHeading", "mdLink", "mdLinkUrl", "mdCode", "mdCodeBlock", "mdCodeBlockBorder", "mdQuote", "mdQuoteBorder", "mdHr", "mdListBullet",
	"toolDiffAdded", "toolDiffRemoved", "toolDiffContext",
	"syntaxComment", "syntaxKeyword", "syntaxFunction", "syntaxVariable", "syntaxString", "syntaxNumber", "syntaxType", "syntaxOperator", "syntaxPunctuation",
	"thinkingOff", "thinkingMinimal", "thinkingLow", "thinkingMedium", "thinkingHigh", "thinkingXhigh",
	"bashMode",
}

var themeBackgroundTokens = map[string]bool{
	"selectedBg":      true,
	"userMessageBg":   true,
	"customMessageBg": true,
	"toolPendingBg":   true,
	"toolSuccessBg":   true,
	"toolErrorBg":     true,
}

func (t ResolvedTheme) Color(token string) string {
	if t.Colors == nil {
		return ""
	}
	return t.Colors[token]
}

func (t ResolvedTheme) Background(token string) string {
	if !themeBackgroundTokens[token] {
		return ""
	}
	return t.Color(token)
}

type interactiveThemeStyles struct {
	Header           lipgloss.Style
	User             lipgloss.Style
	Assistant        lipgloss.Style
	Tool             lipgloss.Style
	ToolOutput       lipgloss.Style
	ToolDiffAdded    lipgloss.Style
	ToolDiffRemoved  lipgloss.Style
	ToolDiffContext  lipgloss.Style
	System           lipgloss.Style
	Error            lipgloss.Style
	Footer           lipgloss.Style
	Suggestion       lipgloss.Style
	Input            lipgloss.Style
	SelectorTitle    lipgloss.Style
	SelectorSelected lipgloss.Style
	SelectorDesc     lipgloss.Style
	SelectorHint     lipgloss.Style
	SelectorTheme    tui.SelectListTheme
	Markdown         tui.MarkdownTheme
}

func defaultInteractiveThemeStyles() interactiveThemeStyles {
	return interactiveThemeStylesFor(DefaultResolvedTheme())
}

func interactiveThemeStylesFor(theme ResolvedTheme) interactiveThemeStyles {
	if theme.Name == "" && len(theme.Colors) == 0 {
		theme = DefaultResolvedTheme()
	}
	token := func(name, fallback string) string {
		if theme.Colors != nil {
			if value, ok := theme.Colors[name]; ok {
				return value
			}
		}
		return fallback
	}
	style := func(name, fallback string) lipgloss.Style {
		color := token(name, fallback)
		out := lipgloss.NewStyle()
		if color != "" {
			out = out.Foreground(lipgloss.Color(color))
		}
		return out
	}
	styleFn := func(name, fallback string) func(string) string {
		s := style(name, fallback)
		return func(text string) string { return s.Render(text) }
	}
	selected := style("accent", "#70A5FF").Bold(true)
	if bg := token("selectedBg", ""); bg != "" {
		selected = selected.Background(lipgloss.Color(bg))
	}
	input := lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true).Padding(0, 1)
	if border := token("borderMuted", "#4F5B66"); border != "" {
		input = input.BorderForeground(lipgloss.Color(border))
	}
	styles := interactiveThemeStyles{
		Header:           style("dim", "#7C8A99"),
		User:             style("userMessageText", "#70A5FF").Bold(true),
		Assistant:        style("text", "#D6DEE8"),
		Tool:             style("toolTitle", "#C6A15B"),
		ToolOutput:       style("toolOutput", "#D6DEE8"),
		ToolDiffAdded:    style("toolDiffAdded", "#8ABEB7"),
		ToolDiffRemoved:  style("toolDiffRemoved", "#CC6666"),
		ToolDiffContext:  style("toolDiffContext", "#7C8A99"),
		System:           style("success", "#87B58B"),
		Error:            style("error", "#FF6B6B"),
		Footer:           style("muted", "#7C8A99"),
		Suggestion:       style("muted", "#9AA7B4"),
		Input:            input,
		SelectorTitle:    style("accent", "#70A5FF").Bold(true),
		SelectorSelected: selected,
		SelectorDesc:     style("muted", "#7C8A99"),
		SelectorHint:     style("muted", "#7C8A99"),
		Markdown: tui.MarkdownTheme{
			Heading:         styleFn("mdHeading", "#f0c674"),
			Link:            styleFn("mdLink", "#81a2be"),
			LinkURL:         styleFn("mdLinkUrl", "#666666"),
			Code:            styleFn("mdCode", "#8abeb7"),
			CodeBlock:       styleFn("mdCodeBlock", "#b5bd68"),
			CodeBlockBorder: styleFn("mdCodeBlockBorder", "#808080"),
			Quote:           styleFn("mdQuote", "#808080"),
			QuoteBorder:     styleFn("mdQuoteBorder", "#808080"),
			HR:              styleFn("mdHr", "#808080"),
			ListBullet:      styleFn("mdListBullet", "#8abeb7"),
		},
	}
	styles.SelectorTheme = tui.SelectListTheme{
		SelectedPrefix: func(s string) string { return s },
		SelectedText:   func(s string) string { return styles.SelectorSelected.Render(s) },
		Description:    func(s string) string { return styles.SelectorDesc.Render(s) },
		ScrollInfo:     func(s string) string { return styles.SelectorDesc.Render(s) },
		NoMatch:        func(s string) string { return styles.SelectorDesc.Render(s) },
	}
	return styles
}

func DefaultResolvedTheme() ResolvedTheme {
	theme, err := builtinResolvedTheme("dark")
	if err != nil {
		return ResolvedTheme{Name: "dark", Colors: map[string]string{"text": "#d4d4d4", "muted": "#808080", "accent": "#8abeb7"}}
	}
	return theme
}

func ResolveTheme(settings *SettingsManager, resources ResourceLoader) (ResolvedTheme, []Diagnostic) {
	selected := ""
	if settings != nil {
		selected = strings.TrimSpace(settings.Theme())
	}
	if selected == "" {
		selected = defaultThemeName()
	}

	candidates := map[string]ResolvedTheme{}
	diagnostics := []Diagnostic{}
	for _, name := range []string{"dark", "light"} {
		theme, err := builtinResolvedTheme(name)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: err.Error()})
			continue
		}
		candidates[theme.Name] = theme
	}

	seenResources := map[string]ResolvedTheme{}
	for _, path := range resources.Themes {
		theme, err := loadResolvedThemeFromPath(path)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: err.Error()})
			continue
		}
		key := theme.Name
		if key == "" {
			key = "unnamed"
		}
		if existing, ok := seenResources[key]; ok {
			diagnostics = append(diagnostics, Diagnostic{
				Type:    DiagWarning,
				Message: fmt.Sprintf(`theme name "%s" collision: keeping %s, ignoring %s`, key, firstNonEmpty(existing.SourcePath, "<builtin>"), firstNonEmpty(theme.SourcePath, "<builtin>")),
			})
			continue
		}
		seenResources[key] = theme
		if _, exists := candidates[key]; !exists {
			candidates[key] = theme
		}
	}

	if theme, ok := candidates[selected]; ok {
		return theme, diagnostics
	}
	for _, theme := range candidates {
		if theme.SourcePath != "" && theme.SourcePath == selected {
			return theme, diagnostics
		}
	}

	if strings.TrimSpace(selected) != "" && selected != "dark" {
		diagnostics = append(diagnostics, Diagnostic{Type: DiagWarning, Message: fmt.Sprintf(`Theme not found: %s; falling back to dark`, selected)})
	}
	if theme, ok := candidates["dark"]; ok {
		return theme, diagnostics
	}
	return DefaultResolvedTheme(), diagnostics
}

func defaultThemeName() string {
	bg := colorFgBgBackgroundIndex(os.Getenv("COLORFGBG"))
	// xterm-256: 231 is pure white (color-cube corner) and 244..255 are the light
	// half of the grayscale ramp; 232..243 are dark grays (232=#080808) and must
	// stay "dark" rather than being lumped in by a blanket bg >= 231 check.
	if bg == 7 || bg == 15 || bg == 231 || bg >= 244 {
		return "light"
	}
	return "dark"
}

func colorFgBgBackgroundIndex(value string) int {
	parts := strings.Split(value, ";")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err == nil && n >= 0 && n <= 255 {
			return n
		}
	}
	return -1
}

func builtinResolvedTheme(name string) (ResolvedTheme, error) {
	switch name {
	case "dark":
		return parseAndResolveTheme("dark", builtinDarkThemeJSON, "")
	case "light":
		return parseAndResolveTheme("light", builtinLightThemeJSON, "")
	default:
		return ResolvedTheme{}, fmt.Errorf("unknown built-in theme: %s", name)
	}
}

func loadResolvedThemeFromPath(path string) (ResolvedTheme, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ResolvedTheme{}, fmt.Errorf("failed to read theme %s: %w", path, err)
	}
	return parseAndResolveTheme(path, string(raw), path)
}

func parseAndResolveTheme(label, raw, sourcePath string) (ResolvedTheme, error) {
	config, err := parseThemeConfig(label, []byte(raw))
	if err != nil {
		return ResolvedTheme{}, err
	}
	config.SourcePath = sourcePath
	return resolveThemeConfig(config)
}

func parseThemeConfig(label string, raw []byte) (ThemeConfig, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ThemeConfig{}, fmt.Errorf("failed to parse theme %s: %w", label, err)
	}
	name, ok := object["name"].(string)
	if !ok {
		return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": missing string field "name"`, label)
	}
	colorsObject, ok := object["colors"].(map[string]any)
	if !ok {
		return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": missing object field "colors"`, label)
	}

	var missing []string
	for _, token := range themeRequiredColorTokens {
		if _, ok := colorsObject[token]; !ok {
			missing = append(missing, token)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return ThemeConfig{}, fmt.Errorf("invalid theme %q: missing required color tokens: %s", label, strings.Join(missing, ", "))
	}

	colors, err := parseThemeValueMap(colorsObject, "colors")
	if err != nil {
		return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": %w`, label, err)
	}
	vars := map[string]themeRawValue{}
	if rawVars, ok := object["vars"]; ok {
		varsObject, ok := rawVars.(map[string]any)
		if !ok {
			return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": vars must be an object`, label)
		}
		vars, err = parseThemeValueMap(varsObject, "vars")
		if err != nil {
			return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": %w`, label, err)
		}
	}
	export := map[string]themeRawValue{}
	if rawExport, ok := object["export"]; ok {
		exportObject, ok := rawExport.(map[string]any)
		if !ok {
			return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": export must be an object`, label)
		}
		export, err = parseThemeValueMap(exportObject, "export")
		if err != nil {
			return ThemeConfig{}, fmt.Errorf(`invalid theme "%s": %w`, label, err)
		}
	}
	return ThemeConfig{Name: name, Vars: vars, Colors: colors, Export: export}, nil
}

func parseThemeValueMap(input map[string]any, label string) (map[string]themeRawValue, error) {
	out := make(map[string]themeRawValue, len(input))
	for key, value := range input {
		parsed, err := parseThemeValue(value)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", label, key, err)
		}
		out[key] = parsed
	}
	return out, nil
}

func parseThemeValue(value any) (themeRawValue, error) {
	switch v := value.(type) {
	case string:
		return themeRawValue{stringValue: v}, nil
	case float64:
		if math.Trunc(v) != v || v < 0 || v > 255 {
			return themeRawValue{}, fmt.Errorf("expected string or integer 0-255")
		}
		return themeRawValue{numberValue: int(v), isNumber: true}, nil
	case int:
		if v < 0 || v > 255 {
			return themeRawValue{}, fmt.Errorf("expected string or integer 0-255")
		}
		return themeRawValue{numberValue: v, isNumber: true}, nil
	default:
		return themeRawValue{}, fmt.Errorf("expected string or integer 0-255")
	}
}

func resolveThemeConfig(config ThemeConfig) (ResolvedTheme, error) {
	colors := make(map[string]string, len(config.Colors))
	for key, value := range config.Colors {
		resolved, err := resolveThemeValue(value, config.Vars, map[string]bool{})
		if err != nil {
			return ResolvedTheme{}, fmt.Errorf(`invalid theme "%s": colors.%s: %w`, config.Name, key, err)
		}
		colors[key] = resolved
	}
	export := make(map[string]string, len(config.Export))
	for key, value := range config.Export {
		resolved, err := resolveThemeValue(value, config.Vars, map[string]bool{})
		if err != nil {
			return ResolvedTheme{}, fmt.Errorf(`invalid theme "%s": export.%s: %w`, config.Name, key, err)
		}
		export[key] = resolved
	}
	return ResolvedTheme{Name: config.Name, SourcePath: config.SourcePath, Colors: colors, Export: export}, nil
}

func resolveThemeValue(value themeRawValue, vars map[string]themeRawValue, visited map[string]bool) (string, error) {
	if value.isNumber {
		return strconv.Itoa(value.numberValue), nil
	}
	raw := value.stringValue
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "#") {
		if !isThemeHexColor(raw) {
			return "", fmt.Errorf("invalid hex color: %s", raw)
		}
		return raw, nil
	}
	if visited[raw] {
		return "", fmt.Errorf("circular variable reference detected: %s", raw)
	}
	ref, ok := vars[raw]
	if !ok {
		return "", fmt.Errorf("variable reference not found: %s", raw)
	}
	visited[raw] = true
	return resolveThemeValue(ref, vars, visited)
}

func isThemeHexColor(value string) bool {
	if len(value) != 7 {
		return false
	}
	for _, r := range value[1:] {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

const builtinDarkThemeJSON = `{
	"name": "dark",
	"vars": {
		"cyan": "#00d7ff", "blue": "#5f87ff", "green": "#b5bd68", "red": "#cc6666", "yellow": "#ffff00",
		"text": "#d4d4d4", "gray": "#808080", "dimGray": "#666666", "darkGray": "#505050", "accent": "#8abeb7",
		"selectedBg": "#3a3a4a", "userMsgBg": "#343541", "toolPendingBg": "#282832", "toolSuccessBg": "#283228",
		"toolErrorBg": "#3c2828", "customMsgBg": "#2d2838"
	},
	"colors": {
		"accent": "accent", "border": "blue", "borderAccent": "cyan", "borderMuted": "darkGray", "success": "green",
		"error": "red", "warning": "yellow", "muted": "gray", "dim": "dimGray", "text": "text", "thinkingText": "gray",
		"selectedBg": "selectedBg", "userMessageBg": "userMsgBg", "userMessageText": "text", "customMessageBg": "customMsgBg",
		"customMessageText": "text", "customMessageLabel": "#9575cd", "toolPendingBg": "toolPendingBg",
		"toolSuccessBg": "toolSuccessBg", "toolErrorBg": "toolErrorBg", "toolTitle": "text", "toolOutput": "gray",
		"mdHeading": "#f0c674", "mdLink": "#81a2be", "mdLinkUrl": "dimGray", "mdCode": "accent", "mdCodeBlock": "green",
		"mdCodeBlockBorder": "gray", "mdQuote": "gray", "mdQuoteBorder": "gray", "mdHr": "gray", "mdListBullet": "accent",
		"toolDiffAdded": "green", "toolDiffRemoved": "red", "toolDiffContext": "gray",
		"syntaxComment": "#6A9955", "syntaxKeyword": "#569CD6", "syntaxFunction": "#DCDCAA", "syntaxVariable": "#9CDCFE",
		"syntaxString": "#CE9178", "syntaxNumber": "#B5CEA8", "syntaxType": "#4EC9B0", "syntaxOperator": "#D4D4D4",
		"syntaxPunctuation": "#D4D4D4", "thinkingOff": "darkGray", "thinkingMinimal": "#6e6e6e", "thinkingLow": "#5f87af",
		"thinkingMedium": "#81a2be", "thinkingHigh": "#b294bb", "thinkingXhigh": "#d183e8", "bashMode": "green"
	},
	"export": { "pageBg": "#18181e", "cardBg": "#1e1e24", "infoBg": "#3c3728" }
}`

const builtinLightThemeJSON = `{
	"name": "light",
	"vars": {
		"teal": "#5a8080", "blue": "#547da7", "green": "#588458", "red": "#aa5555", "yellow": "#9a7326",
		"text": "#1f2328", "mediumGray": "#6c6c6c", "dimGray": "#767676", "lightGray": "#b0b0b0",
		"selectedBg": "#d0d0e0", "userMsgBg": "#e8e8e8", "toolPendingBg": "#e8e8f0", "toolSuccessBg": "#e8f0e8",
		"toolErrorBg": "#f0e8e8", "customMsgBg": "#ede7f6"
	},
	"colors": {
		"accent": "teal", "border": "blue", "borderAccent": "teal", "borderMuted": "lightGray", "success": "green",
		"error": "red", "warning": "yellow", "muted": "mediumGray", "dim": "dimGray", "text": "text",
		"thinkingText": "mediumGray", "selectedBg": "selectedBg", "userMessageBg": "userMsgBg", "userMessageText": "text",
		"customMessageBg": "customMsgBg", "customMessageText": "text", "customMessageLabel": "#7e57c2",
		"toolPendingBg": "toolPendingBg", "toolSuccessBg": "toolSuccessBg", "toolErrorBg": "toolErrorBg", "toolTitle": "text",
		"toolOutput": "mediumGray", "mdHeading": "yellow", "mdLink": "blue", "mdLinkUrl": "dimGray", "mdCode": "teal",
		"mdCodeBlock": "green", "mdCodeBlockBorder": "mediumGray", "mdQuote": "mediumGray", "mdQuoteBorder": "mediumGray",
		"mdHr": "mediumGray", "mdListBullet": "green", "toolDiffAdded": "green", "toolDiffRemoved": "red",
		"toolDiffContext": "mediumGray", "syntaxComment": "#008000", "syntaxKeyword": "#0000FF", "syntaxFunction": "#795E26",
		"syntaxVariable": "#001080", "syntaxString": "#A31515", "syntaxNumber": "#098658", "syntaxType": "#267F99",
		"syntaxOperator": "#000000", "syntaxPunctuation": "#000000", "thinkingOff": "lightGray", "thinkingMinimal": "#767676",
		"thinkingLow": "blue", "thinkingMedium": "teal", "thinkingHigh": "#875f87", "thinkingXhigh": "#8b008b",
		"bashMode": "green"
	},
	"export": { "pageBg": "#f8f8f8", "cardBg": "#ffffff", "infoBg": "#fffae6" }
}`
