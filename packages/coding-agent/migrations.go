package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err := MigrateSessionsFromAgentRoot(agentDir); err != nil {
		return MigrationResult{MigratedAuthProviders: providers}, err
	}
	if err := MigrateToolsToBin(agentDir); err != nil {
		return MigrationResult{MigratedAuthProviders: providers}, err
	}
	return MigrationResult{
		MigratedAuthProviders: providers,
		DeprecationWarnings:   MigrateExtensionSystem(cwd, agentDir),
	}, nil
}

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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "\t")
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
