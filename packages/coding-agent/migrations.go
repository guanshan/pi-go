package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

const (
	MigrationGuideURL = "https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
	ExtensionsDocURL  = "https://github.com/earendil-works/pi-mono/blob/main/packages/coding-agent/docs/extensions.md"
)

type MigrationResult struct {
	MigratedAuthProviders []string `json:"migratedAuthProviders"`
	DeprecationWarnings   []string `json:"deprecationWarnings"`
}

func MigrateAuthToAuthJSON(agentDirOpt ...string) ([]string, error) {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	authPath := filepath.Join(agentDir, "auth.json")
	oauthPath := filepath.Join(agentDir, "oauth.json")
	settingsPath := filepath.Join(agentDir, "settings.json")
	if fileExists(authPath) {
		return nil, nil
	}

	migrated := map[string]json.RawMessage{}
	var providers []string
	if raw, err := os.ReadFile(oauthPath); err == nil {
		var oauth map[string]json.RawMessage
		if err := json.Unmarshal(raw, &oauth); err == nil {
			for provider, cred := range oauth {
				object := map[string]json.RawMessage{}
				_ = json.Unmarshal(cred, &object)
				object["type"] = json.RawMessage(`"oauth"`)
				wrapped, err := json.Marshal(object)
				if err != nil {
					continue
				}
				migrated[provider] = wrapped
				providers = append(providers, provider)
			}
			_ = os.Rename(oauthPath, oauthPath+".migrated")
		}
	}

	if raw, err := os.ReadFile(settingsPath); err == nil {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(raw, &settings); err == nil {
			var apiKeys map[string]string
			if err := json.Unmarshal(settings["apiKeys"], &apiKeys); err == nil && len(apiKeys) > 0 {
				for provider, key := range apiKeys {
					if _, exists := migrated[provider]; exists || key == "" {
						continue
					}
					wrapped, err := json.Marshal(map[string]string{"type": "api_key", "key": key})
					if err != nil {
						continue
					}
					migrated[provider] = wrapped
					providers = append(providers, provider)
				}
				delete(settings, "apiKeys")
				if err := writeIndentedJSON(settingsPath, settings, 0o600); err != nil {
					return providers, err
				}
			}
		}
	}

	if len(migrated) == 0 {
		return providers, nil
	}
	if err := writeIndentedJSON(authPath, migrated, 0o600); err != nil {
		return providers, err
	}
	return providers, nil
}

func MigrateSessionsFromAgentRoot(agentDirOpt ...string) error {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(agentDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		firstLine := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
		if firstLine == "" {
			continue
		}
		var header struct {
			Type string `json:"type"`
			CWD  string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(firstLine), &header); err != nil || header.Type != "session" || header.CWD == "" {
			continue
		}
		targetDir := filepath.Join(agentDir, "sessions", encodeMigrationCWD(header.CWD))
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return err
		}
		target := filepath.Join(targetDir, entry.Name())
		if fileExists(target) {
			continue
		}
		_ = os.Rename(path, target)
	}
	return nil
}

func MigrateCommandsToPrompts(baseDir, _ string) bool {
	commandsDir := filepath.Join(baseDir, "commands")
	promptsDir := filepath.Join(baseDir, "prompts")
	if dirExists(commandsDir) && !dirExists(promptsDir) {
		return os.Rename(commandsDir, promptsDir) == nil
	}
	return false
}

func MigrateToolsToBin(agentDirOpt ...string) error {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	toolsDir := filepath.Join(agentDir, "tools")
	if !dirExists(toolsDir) {
		return nil
	}
	binDir := filepath.Join(agentDir, "bin")
	for _, name := range []string{"fd", "rg", "fd.exe", "rg.exe"} {
		oldPath := filepath.Join(toolsDir, name)
		if !fileExists(oldPath) {
			continue
		}
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return err
		}
		newPath := filepath.Join(binDir, name)
		if fileExists(newPath) {
			_ = os.Remove(oldPath)
			continue
		}
		_ = os.Rename(oldPath, newPath)
	}
	return nil
}

func CheckDeprecatedExtensionDirs(baseDir, label string) []string {
	var warnings []string
	if dirExists(filepath.Join(baseDir, "hooks")) {
		warnings = append(warnings, label+" hooks/ directory found. Hooks have been renamed to extensions.")
	}
	toolsDir := filepath.Join(baseDir, "tools")
	if dirExists(toolsDir) {
		entries, err := os.ReadDir(toolsDir)
		if err == nil {
			for _, entry := range entries {
				lower := strings.ToLower(entry.Name())
				if lower == "fd" || lower == "rg" || lower == "fd.exe" || lower == "rg.exe" || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				warnings = append(warnings, label+" tools/ directory contains custom tools. Custom tools have been merged into extensions.")
				break
			}
		}
	}
	return warnings
}

func MigrateExtensionSystem(cwd, agentDir string) []string {
	if agentDir == "" {
		agentDir = core.AgentDir()
	}
	projectDir := core.ProjectPiDir(cwd)
	MigrateCommandsToPrompts(agentDir, "Global")
	MigrateCommandsToPrompts(projectDir, "Project")
	warnings := CheckDeprecatedExtensionDirs(agentDir, "Global")
	warnings = append(warnings, CheckDeprecatedExtensionDirs(projectDir, "Project")...)
	return warnings
}

func RunMigrations(cwd string, agentDirOpt ...string) (MigrationResult, error) {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	providers, err := MigrateAuthToAuthJSON(agentDir)
	if err != nil {
		return MigrationResult{MigratedAuthProviders: providers}, err
	}
	MigrateExplicitEnvVarConfigValues(agentDir)
	if err := MigrateSessionsFromAgentRoot(agentDir); err != nil {
		return MigrationResult{MigratedAuthProviders: providers}, err
	}
	if err := MigrateToolsToBin(agentDir); err != nil {
		return MigrationResult{MigratedAuthProviders: providers}, err
	}
	MigrateKeybindingsConfigFile(agentDir)
	return MigrationResult{
		MigratedAuthProviders: providers,
		DeprecationWarnings:   MigrateExtensionSystem(cwd, agentDir),
	}, nil
}

// legacyEnvVarNameRE matches a config value that is a bare legacy environment
// variable name (e.g. "OPENAI_API_KEY"). Mirrors LEGACY_ENV_VAR_NAME_RE in
// src/core/resolve-config-value.ts.
var legacyEnvVarNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// isLegacyEnvVarNameConfigValue reports whether value names an environment
// variable in the legacy bare-name form that newer config requires to be
// written as "$VALUE". Mirrors isLegacyEnvVarNameConfigValue in the TS port.
func isLegacyEnvVarNameConfigValue(value string) bool {
	return legacyEnvVarNameRE.MatchString(value)
}

// MigrateExplicitEnvVarConfigValues rewrites auth.json and models.json values
// that name an environment variable (bare uppercase form) into the explicit
// "$ENV_VAR" syntax, so plain strings can be treated as literals. Mirrors
// migrateExplicitEnvVarConfigValues in src/migrations.ts (migrations.ts:190).
func MigrateExplicitEnvVarConfigValues(agentDirOpt ...string) {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	migrateAuthJSONConfigValues(agentDir)
	migrateModelsJSONConfigValues(agentDir)
}

// migrateAuthJSONConfigValues rewrites api_key credential keys in auth.json that
// are bare env-var names into "$ENV_VAR" form. The file is rewritten (0600)
// only when at least one value changes. Mirrors migrateAuthJsonConfigValues.
func migrateAuthJSONConfigValues(agentDir string) {
	authPath := filepath.Join(agentDir, "auth.json")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	migrated := false
	for provider, credRaw := range data {
		var cred map[string]json.RawMessage
		if err := json.Unmarshal(credRaw, &cred); err != nil {
			continue
		}
		var credType string
		if err := json.Unmarshal(cred["type"], &credType); err != nil || credType != "api_key" {
			continue
		}
		if migrateLegacyEnvVarProperty(cred, "key") {
			wrapped, err := json.Marshal(cred)
			if err != nil {
				continue
			}
			data[provider] = wrapped
			migrated = true
		}
	}
	if !migrated {
		return
	}
	_ = writeIndentedJSON(authPath, data, 0o600)
}

// migrateModelsJSONConfigValues rewrites provider apiKey and header values in
// models.json that are bare env-var names into "$ENV_VAR" form. models.json may
// contain // line comments and trailing commas, so it is parsed JSONC-tolerant.
// Mirrors migrateModelsJsonConfigValues.
func migrateModelsJSONConfigValues(agentDir string) {
	modelsPath := filepath.Join(agentDir, "models.json")
	raw, err := os.ReadFile(modelsPath)
	if err != nil {
		return
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(stripJSONComments(raw), &top); err != nil {
		return
	}
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(top["providers"], &providers); err != nil || len(providers) == 0 {
		return
	}
	migrated := false
	for provider, providerRaw := range providers {
		var providerConfig map[string]json.RawMessage
		if err := json.Unmarshal(providerRaw, &providerConfig); err != nil {
			continue
		}
		changed := migrateLegacyEnvVarProperty(providerConfig, "apiKey")
		changed = migrateLegacyEnvVarHeaders(providerConfig, "headers") || changed
		changed = migrateLegacyEnvVarModelHeaders(providerConfig, "models") || changed
		changed = migrateLegacyEnvVarOverrideHeaders(providerConfig, "modelOverrides") || changed
		if !changed {
			continue
		}
		wrapped, err := json.Marshal(providerConfig)
		if err != nil {
			continue
		}
		providers[provider] = wrapped
		migrated = true
	}
	if !migrated {
		return
	}
	top["providers"], err = json.Marshal(providers)
	if err != nil {
		return
	}
	_ = writeIndentedJSON(modelsPath, top, 0o644)
}

// migrateLegacyEnvVarProperty rewrites record[key] in place when it is a string
// in the legacy bare env-var form. Returns true when a rewrite happened.
func migrateLegacyEnvVarProperty(record map[string]json.RawMessage, key string) bool {
	rawValue, ok := record[key]
	if !ok {
		return false
	}
	var value string
	if err := json.Unmarshal(rawValue, &value); err != nil {
		return false
	}
	if !isLegacyEnvVarNameConfigValue(value) {
		return false
	}
	wrapped, err := json.Marshal("$" + value)
	if err != nil {
		return false
	}
	record[key] = wrapped
	return true
}

// migrateLegacyEnvVarHeaders rewrites every string header value in record[key]
// that is a bare env-var name. Returns true when any value changed.
func migrateLegacyEnvVarHeaders(record map[string]json.RawMessage, key string) bool {
	rawHeaders, ok := record[key]
	if !ok {
		return false
	}
	var headers map[string]json.RawMessage
	if err := json.Unmarshal(rawHeaders, &headers); err != nil {
		return false
	}
	migrated := false
	for headerKey := range headers {
		if migrateLegacyEnvVarProperty(headers, headerKey) {
			migrated = true
		}
	}
	if !migrated {
		return false
	}
	wrapped, err := json.Marshal(headers)
	if err != nil {
		return false
	}
	record[key] = wrapped
	return true
}

// migrateLegacyEnvVarModelHeaders rewrites header values on each entry of the
// provider's models array.
func migrateLegacyEnvVarModelHeaders(record map[string]json.RawMessage, key string) bool {
	rawModels, ok := record[key]
	if !ok {
		return false
	}
	var models []json.RawMessage
	if err := json.Unmarshal(rawModels, &models); err != nil {
		return false
	}
	migrated := false
	for index, modelRaw := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(modelRaw, &model); err != nil {
			continue
		}
		if migrateLegacyEnvVarHeaders(model, "headers") {
			wrapped, err := json.Marshal(model)
			if err != nil {
				continue
			}
			models[index] = wrapped
			migrated = true
		}
	}
	if !migrated {
		return false
	}
	wrapped, err := json.Marshal(models)
	if err != nil {
		return false
	}
	record[key] = wrapped
	return true
}

// migrateLegacyEnvVarOverrideHeaders rewrites header values on each entry of the
// provider's modelOverrides map.
func migrateLegacyEnvVarOverrideHeaders(record map[string]json.RawMessage, key string) bool {
	rawOverrides, ok := record[key]
	if !ok {
		return false
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal(rawOverrides, &overrides); err != nil {
		return false
	}
	migrated := false
	for overrideKey, overrideRaw := range overrides {
		var override map[string]json.RawMessage
		if err := json.Unmarshal(overrideRaw, &override); err != nil {
			continue
		}
		if migrateLegacyEnvVarHeaders(override, "headers") {
			wrapped, err := json.Marshal(override)
			if err != nil {
				continue
			}
			overrides[overrideKey] = wrapped
			migrated = true
		}
	}
	if !migrated {
		return false
	}
	wrapped, err := json.Marshal(overrides)
	if err != nil {
		return false
	}
	record[key] = wrapped
	return true
}

// stripJSONComments removes // line comments and trailing commas (before } or
// ]) from JSONC input, leaving string literals untouched. Mirrors
// stripJsonComments in src/utils/json.ts.
func stripJSONComments(input []byte) []byte {
	out := make([]byte, 0, len(input))
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(input) && input[i+1] == '/' {
			// Skip to end of line.
			for i < len(input) && input[i] != '\n' {
				i++
			}
			if i < len(input) {
				out = append(out, input[i])
			}
			continue
		}
		if c == ',' {
			// Drop the comma when the next non-whitespace byte closes an
			// object or array.
			j := i + 1
			for j < len(input) && (input[j] == ' ' || input[j] == '\t' || input[j] == '\n' || input[j] == '\r') {
				j++
			}
			if j < len(input) && (input[j] == '}' || input[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// keybindingNameMigrations maps legacy keybinding names to their current names.
// Mirrors KEYBINDING_NAME_MIGRATIONS in src/core/keybindings.ts.
var keybindingNameMigrations = map[string]string{
	"cursorUp":                 "tui.editor.cursorUp",
	"cursorDown":               "tui.editor.cursorDown",
	"cursorLeft":               "tui.editor.cursorLeft",
	"cursorRight":              "tui.editor.cursorRight",
	"cursorWordLeft":           "tui.editor.cursorWordLeft",
	"cursorWordRight":          "tui.editor.cursorWordRight",
	"cursorLineStart":          "tui.editor.cursorLineStart",
	"cursorLineEnd":            "tui.editor.cursorLineEnd",
	"jumpForward":              "tui.editor.jumpForward",
	"jumpBackward":             "tui.editor.jumpBackward",
	"pageUp":                   "tui.editor.pageUp",
	"pageDown":                 "tui.editor.pageDown",
	"deleteCharBackward":       "tui.editor.deleteCharBackward",
	"deleteCharForward":        "tui.editor.deleteCharForward",
	"deleteWordBackward":       "tui.editor.deleteWordBackward",
	"deleteWordForward":        "tui.editor.deleteWordForward",
	"deleteToLineStart":        "tui.editor.deleteToLineStart",
	"deleteToLineEnd":          "tui.editor.deleteToLineEnd",
	"yank":                     "tui.editor.yank",
	"yankPop":                  "tui.editor.yankPop",
	"undo":                     "tui.editor.undo",
	"newLine":                  "tui.input.newLine",
	"submit":                   "tui.input.submit",
	"tab":                      "tui.input.tab",
	"copy":                     "tui.input.copy",
	"selectUp":                 "tui.select.up",
	"selectDown":               "tui.select.down",
	"selectPageUp":             "tui.select.pageUp",
	"selectPageDown":           "tui.select.pageDown",
	"selectConfirm":            "tui.select.confirm",
	"selectCancel":             "tui.select.cancel",
	"interrupt":                "app.interrupt",
	"clear":                    "app.clear",
	"exit":                     "app.exit",
	"suspend":                  "app.suspend",
	"cycleThinkingLevel":       "app.thinking.cycle",
	"cycleModelForward":        "app.model.cycleForward",
	"cycleModelBackward":       "app.model.cycleBackward",
	"selectModel":              "app.model.select",
	"expandTools":              "app.tools.expand",
	"toggleThinking":           "app.thinking.toggle",
	"toggleSessionNamedFilter": "app.session.toggleNamedFilter",
	"externalEditor":           "app.editor.external",
	"followUp":                 "app.message.followUp",
	"dequeue":                  "app.message.dequeue",
	"pasteImage":               "app.clipboard.pasteImage",
	"newSession":               "app.session.new",
	"tree":                     "app.session.tree",
	"fork":                     "app.session.fork",
	"resume":                   "app.session.resume",
	"treeFoldOrUp":             "app.tree.foldOrUp",
	"treeUnfoldOrDown":         "app.tree.unfoldOrDown",
	"treeEditLabel":            "app.tree.editLabel",
	"treeToggleLabelTimestamp": "app.tree.toggleLabelTimestamp",
	"toggleSessionPath":        "app.session.togglePath",
	"toggleSessionSort":        "app.session.toggleSort",
	"renameSession":            "app.session.rename",
	"deleteSession":            "app.session.delete",
	"deleteSessionNoninvasive": "app.session.deleteNoninvasive",
}

// keybindingOrder is the canonical key order of KEYBINDINGS in
// src/core/keybindings.ts (TUI keybindings first, then app.* bindings). Used to
// order keybindings.json on migration write-back, matching orderKeybindingsConfig.
var keybindingOrder = []string{
	"tui.editor.cursorUp",
	"tui.editor.cursorDown",
	"tui.editor.cursorLeft",
	"tui.editor.cursorRight",
	"tui.editor.cursorWordLeft",
	"tui.editor.cursorWordRight",
	"tui.editor.cursorLineStart",
	"tui.editor.cursorLineEnd",
	"tui.editor.jumpForward",
	"tui.editor.jumpBackward",
	"tui.editor.pageUp",
	"tui.editor.pageDown",
	"tui.editor.deleteCharBackward",
	"tui.editor.deleteCharForward",
	"tui.editor.deleteWordBackward",
	"tui.editor.deleteWordForward",
	"tui.editor.deleteToLineStart",
	"tui.editor.deleteToLineEnd",
	"tui.editor.yank",
	"tui.editor.yankPop",
	"tui.editor.undo",
	"tui.input.newLine",
	"tui.input.submit",
	"tui.input.tab",
	"tui.input.copy",
	"tui.select.up",
	"tui.select.down",
	"tui.select.pageUp",
	"tui.select.pageDown",
	"tui.select.confirm",
	"tui.select.cancel",
	"app.interrupt",
	"app.clear",
	"app.exit",
	"app.suspend",
	"app.thinking.cycle",
	"app.model.cycleForward",
	"app.model.cycleBackward",
	"app.model.select",
	"app.tools.expand",
	"app.thinking.toggle",
	"app.session.toggleNamedFilter",
	"app.editor.external",
	"app.message.followUp",
	"app.message.dequeue",
	"app.clipboard.pasteImage",
	"app.session.new",
	"app.session.tree",
	"app.session.fork",
	"app.session.resume",
	"app.tree.foldOrUp",
	"app.tree.unfoldOrDown",
	"app.tree.editLabel",
	"app.tree.toggleLabelTimestamp",
	"app.session.togglePath",
	"app.session.toggleSort",
	"app.session.rename",
	"app.session.delete",
	"app.session.deleteNoninvasive",
	"app.models.save",
	"app.models.enableAll",
	"app.models.clearAll",
	"app.models.toggleProvider",
	"app.models.reorderUp",
	"app.models.reorderDown",
	"app.tree.filter.default",
	"app.tree.filter.noTools",
	"app.tree.filter.userOnly",
	"app.tree.filter.labeledOnly",
	"app.tree.filter.all",
	"app.tree.filter.cycleForward",
	"app.tree.filter.cycleBackward",
}

// MigrateKeybindingsConfigFile rewrites keybindings.json, renaming legacy
// keybinding names to their current names (dropping a legacy entry when the
// renamed key already exists) and re-ordering keys to the canonical order. The
// file is rewritten only when a rename occurred. Mirrors
// migrateKeybindingsConfigFile in src/migrations.ts (migrations.ts:288).
func MigrateKeybindingsConfigFile(agentDirOpt ...string) {
	agentDir := core.AgentDir()
	if len(agentDirOpt) > 0 && agentDirOpt[0] != "" {
		agentDir = agentDirOpt[0]
	}
	configPath := filepath.Join(agentDir, "keybindings.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var rawConfig map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawConfig); err != nil {
		return
	}
	config, migrated := migrateKeybindingsConfig(rawConfig)
	if !migrated {
		return
	}
	_ = writeOrderedKeybindings(configPath, config)
}

// migrateKeybindingsConfig renames legacy keybinding keys to their current
// names, dropping a legacy entry when the target name is already present.
// Mirrors migrateKeybindingsConfig in src/core/keybindings.ts.
func migrateKeybindingsConfig(rawConfig map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	config := make(map[string]json.RawMessage, len(rawConfig))
	migrated := false
	for key, value := range rawConfig {
		nextKey := key
		if mapped, ok := keybindingNameMigrations[key]; ok {
			nextKey = mapped
		}
		if nextKey != key {
			migrated = true
			if _, exists := rawConfig[nextKey]; exists {
				continue
			}
		}
		config[nextKey] = value
	}
	return config, migrated
}

// writeOrderedKeybindings writes the keybindings config ordered by the canonical
// keybindingOrder, with any unknown extras appended in sorted order, using
// 2-space indentation. Mirrors orderKeybindingsConfig +
// writeFileSync(JSON.stringify(config, null, 2)).
func writeOrderedKeybindings(path string, config map[string]json.RawMessage) error {
	keys := make([]string, 0, len(config))
	for _, key := range keybindingOrder {
		if _, ok := config[key]; ok {
			keys = append(keys, key)
		}
	}
	var extras []string
	for key := range config {
		if _, known := keybindingOrderSet[key]; !known {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	keys = append(keys, extras...)

	var buf strings.Builder
	if len(keys) == 0 {
		buf.WriteString("{}\n")
	} else {
		buf.WriteString("{")
		for index, key := range keys {
			if index > 0 {
				buf.WriteString(",")
			}
			keyJSON, _ := json.Marshal(key)
			buf.WriteString("\n  ")
			buf.Write(keyJSON)
			buf.WriteString(": ")
			buf.Write(indentNestedJSON(config[key]))
		}
		buf.WriteString("\n}\n")
	}
	// keybindings.json holds no secrets; match the TS writeFileSync defaults
	// rather than the 0700/0600 credential boundary.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// indentNestedJSON renders a keybindings value (string or string array) with
// 2-space indentation continuing from one level of nesting, matching
// JSON.stringify(config, null, 2).
func indentNestedJSON(raw json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	out, err := json.MarshalIndent(value, "  ", "  ")
	if err != nil {
		return raw
	}
	return out
}

// keybindingOrderSet indexes keybindingOrder for membership tests.
var keybindingOrderSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(keybindingOrder))
	for _, key := range keybindingOrder {
		set[key] = struct{}{}
	}
	return set
}()

func ShowDeprecationWarnings(ctx context.Context, warnings []string) error {
	for _, warning := range warnings {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		fmt.Fprintln(os.Stderr, "Warning:", warning)
	}
	return nil
}

func encodeMigrationCWD(cwd string) string {
	clean := strings.TrimPrefix(strings.TrimPrefix(filepath.Clean(cwd), "/"), `\`)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(clean) + "--"
}

func writeIndentedJSON(path string, value any, mode os.FileMode) error {
	// Derive the parent-directory mode from the file's sensitivity: a 0600 file
	// (auth/settings credentials) gets a 0700 directory so the credential
	// boundary is not world-traversable, matching src/core/auth-storage.ts.
	dirMode := os.FileMode(0o755)
	if mode&0o077 == 0 {
		dirMode = 0o700
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	if dirMode&0o077 == 0 {
		if err := os.Chmod(dir, dirMode); err != nil {
			return err
		}
	}
	// TS writes migrated settings/auth with 2-space indent (migrations.ts:62,71).
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, mode)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
