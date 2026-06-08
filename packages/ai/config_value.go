package ai

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// config_value.go ports packages/coding-agent/src/core/resolve-config-value.ts.
//
// It resolves configuration values (custom-provider API keys, header values)
// that may be:
//   - a shell command, when the value starts with "!" (the rest is executed and
//     its trimmed stdout is used; results are cached for the process lifetime
//     with a 10s execution timeout);
//   - an environment-variable template using "$VAR" / "${VAR}" references,
//     interpolated into the surrounding literal text (e.g. "Bearer $TOKEN");
//   - a literal, where "$$" escapes a literal "$" and "$!" escapes a literal
//     "!".
//
// Built-in providers never set a configured apiKey/header string, so they are
// unaffected: ResolveConfigValue("") returns ("", false).

var (
	configValueEnvVarNameRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	configValueEnvVarNamePrefixRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// commandResultCache caches "!command" stdout for the process lifetime, mirroring
// the TS commandResultCache Map. A present entry with ok==false records a command
// that ran but produced no usable output (so it is not re-executed).
var (
	commandResultCacheMu sync.Mutex
	commandResultCache   = map[string]configValueCacheEntry{}
)

type configValueCacheEntry struct {
	value string
	ok    bool
}

type configTemplatePartKind int

const (
	configTemplateLiteral configTemplatePartKind = iota
	configTemplateEnv
)

type configTemplatePart struct {
	kind  configTemplatePartKind
	value string // literal text, or env-var name for env parts
}

type configValueReference struct {
	command bool
	config  string // original config string (for command references)
	parts   []configTemplatePart
}

func appendConfigLiteral(parts []configTemplatePart, value string) []configTemplatePart {
	if value == "" {
		return parts
	}
	if n := len(parts); n > 0 && parts[n-1].kind == configTemplateLiteral {
		parts[n-1].value += value
		return parts
	}
	return append(parts, configTemplatePart{kind: configTemplateLiteral, value: value})
}

func parseConfigValueTemplate(config string) []configTemplatePart {
	var parts []configTemplatePart
	index := 0
	for index < len(config) {
		dollarIndex := strings.IndexByte(config[index:], '$')
		if dollarIndex < 0 {
			parts = appendConfigLiteral(parts, config[index:])
			break
		}
		dollarIndex += index
		parts = appendConfigLiteral(parts, config[index:dollarIndex])

		var nextChar byte
		if dollarIndex+1 < len(config) {
			nextChar = config[dollarIndex+1]
		}

		if nextChar == '$' || nextChar == '!' {
			parts = appendConfigLiteral(parts, string(nextChar))
			index = dollarIndex + 2
			continue
		}

		if nextChar == '{' {
			endIndex := strings.IndexByte(config[dollarIndex+2:], '}')
			if endIndex < 0 {
				parts = appendConfigLiteral(parts, "$")
				index = dollarIndex + 1
				continue
			}
			endIndex += dollarIndex + 2
			name := config[dollarIndex+2 : endIndex]
			if configValueEnvVarNameRE.MatchString(name) {
				parts = append(parts, configTemplatePart{kind: configTemplateEnv, value: name})
			} else {
				parts = appendConfigLiteral(parts, config[dollarIndex:endIndex+1])
			}
			index = endIndex + 1
			continue
		}

		if match := configValueEnvVarNamePrefixRE.FindString(config[dollarIndex+1:]); match != "" {
			parts = append(parts, configTemplatePart{kind: configTemplateEnv, value: match})
			index = dollarIndex + 1 + len(match)
			continue
		}

		parts = appendConfigLiteral(parts, "$")
		index = dollarIndex + 1
	}
	return parts
}

func parseConfigValueReference(config string) configValueReference {
	if strings.HasPrefix(config, "!") {
		return configValueReference{command: true, config: config}
	}
	return configValueReference{parts: parseConfigValueTemplate(config)}
}

func resolveEnvConfigValue(name string) (string, bool) {
	value := os.Getenv(name)
	if value == "" {
		return "", false
	}
	return value, true
}

func resolveConfigTemplate(parts []configTemplatePart) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		if part.kind == configTemplateLiteral {
			b.WriteString(part.value)
			continue
		}
		value, ok := resolveEnvConfigValue(part.value)
		if !ok {
			return "", false
		}
		b.WriteString(value)
	}
	return b.String(), true
}

// ConfigValueEnvVarName returns the single environment-variable name when the
// config value is exactly one "$VAR" / "${VAR}" reference, mirroring TS
// getConfigValueEnvVarName. Returns "" otherwise (commands, multi-part
// templates, literals).
func ConfigValueEnvVarName(config string) string {
	ref := parseConfigValueReference(config)
	if ref.command {
		return ""
	}
	if len(ref.parts) == 1 && ref.parts[0].kind == configTemplateEnv {
		return ref.parts[0].value
	}
	return ""
}

// IsCommandConfigValue reports whether the config value is a "!command" form.
func IsCommandConfigValue(config string) bool {
	return parseConfigValueReference(config).command
}

// ResolveConfigValue resolves a config value to its actual value, returning
// (value, true) on success or ("", false) when an env var is missing or a
// command fails. Command results are cached for the process lifetime. Mirrors TS
// resolveConfigValue.
func ResolveConfigValue(config string) (string, bool) {
	if config == "" {
		return "", false
	}
	ref := parseConfigValueReference(config)
	if ref.command {
		return executeConfigCommand(ref.config)
	}
	return resolveConfigTemplate(ref.parts)
}

// ResolveConfigValueUncached behaves like ResolveConfigValue but bypasses the
// command result cache, mirroring TS resolveConfigValueUncached.
func ResolveConfigValueUncached(config string) (string, bool) {
	if config == "" {
		return "", false
	}
	ref := parseConfigValueReference(config)
	if ref.command {
		return executeConfigCommandUncached(ref.config)
	}
	return resolveConfigTemplate(ref.parts)
}

func executeConfigCommand(commandConfig string) (string, bool) {
	commandResultCacheMu.Lock()
	if entry, ok := commandResultCache[commandConfig]; ok {
		commandResultCacheMu.Unlock()
		return entry.value, entry.ok
	}
	commandResultCacheMu.Unlock()

	value, ok := executeConfigCommandUncached(commandConfig)

	commandResultCacheMu.Lock()
	commandResultCache[commandConfig] = configValueCacheEntry{value: value, ok: ok}
	commandResultCacheMu.Unlock()
	return value, ok
}

func executeConfigCommandUncached(commandConfig string) (string, bool) {
	// Strip the leading "!".
	command := commandConfig[1:]
	return runConfigShellCommand(command)
}

// runConfigShellCommand executes command via the system shell with a 10s
// timeout, returning the trimmed stdout. Mirrors TS executeWithDefaultShell
// (Node execSync, which uses /bin/sh -c on Unix and cmd.exe on Windows). An
// empty trimmed output, a non-zero exit, or a launch failure all yield
// ("", false).
func runConfigShellCommand(command string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
	}
	cmd.Stdin = nil
	cmd.Stderr = nil

	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", false
	}
	return value, true
}

// ClearConfigValueCache clears the command-result cache. Exposed for tests,
// mirroring TS clearConfigValueCache.
func ClearConfigValueCache() {
	commandResultCacheMu.Lock()
	commandResultCache = map[string]configValueCacheEntry{}
	commandResultCacheMu.Unlock()
}
