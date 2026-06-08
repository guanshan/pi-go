package core

import (
	"slices"
	"sort"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

const DefaultAgentMaxLoop = 25

type ToolSet map[string]catools.RuntimeTool

func BuiltinTools(cwd string, settings *SettingsManager) ToolSet {
	// Default to vision-capable so callers without a concrete model keep the prior
	// behavior (no spurious non-vision image note).
	return BuiltinToolsForModel(cwd, settings, true)
}

// BuiltinToolsForModel builds the builtin toolset with the active model's image
// capability so the read tool can mirror read.ts getNonVisionImageNote.
func BuiltinToolsForModel(cwd string, settings *SettingsManager, modelSupportsImages bool) ToolSet {
	options := catools.BuiltinToolOptions{AutoResize: true, BinDir: settingsBinDir(settings), ModelSupportsImages: modelSupportsImages}
	if settings != nil {
		options.ShellPath = settings.mergedString(settings.Global.ShellPath, settings.Project.ShellPath, "")
		options.CommandPrefix = settings.ShellCommandPrefix()
		options.AutoResize = settings.ImageAutoResize()
	}
	return ToolSet(catools.BuiltinTools(cwd, options))
}

func settingsBinDir(settings *SettingsManager) string {
	if settings != nil && settings.AgentDir != "" {
		return BinDirForAgentDir(settings.AgentDir)
	}
	return BinDir()
}

func agentSessionBinDir(agent *AgentSession) string {
	if agent != nil {
		return settingsBinDir(agent.Settings)
	}
	return BinDir()
}

func FilterTools(all ToolSet, args cli.Args) ToolSet {
	return ToolSet(catools.FilterTools(catools.ToolSet(all), catools.FilterOptions{
		NoTools:        args.NoTools,
		Tools:          args.Tools,
		NoBuiltinTools: args.NoBuiltinTools,
	}))
}

// builtinToolOrder is the canonical registration order of the builtin tools,
// matching the TS state.tools registration order that getActiveToolNames returns
// (agent-session.ts). The system prompt lists tools and collects their guideline
// bullets in this order, so it must NOT be sorted or the byte-shape diverges.
var builtinToolOrder = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

// ToolPromptInfo carries the per-tool data the system-prompt builder needs: the
// present tool names in registration order, their one-line snippets, and the
// collected guideline bullets (in tool order). It is the Go analogue of the TS
// buildSystemPrompt options (selectedTools / toolSnippets / promptGuidelines).
type ToolPromptInfo struct {
	// OrderedNames are the present tool names: builtins in builtinToolOrder, then
	// any custom/extension tools in sorted order.
	OrderedNames []string
	// Snippets maps a present tool name to its one-line prompt snippet, only for
	// tools that expose a non-empty one via catools.PromptMetadata.
	Snippets map[string]string
	// Guidelines are the per-tool guideline bullets in OrderedNames order (before
	// the builder dedups them and appends the always-on bullets).
	Guidelines []string
}

// Has reports whether the named tool is present.
func (i ToolPromptInfo) Has(name string) bool {
	return slices.Contains(i.OrderedNames, name)
}

// orderedToolNames returns the tool names in canonical builtin order, with any
// non-builtin (custom/extension) tools appended in sorted order for determinism.
// The builtin order is a byte-exact match with the TS registration order; custom
// tools are sorted rather than kept in registration order (a minor, deliberate
// divergence — the ToolSet is a map with no insertion order, and custom tools
// rarely expose prompt snippets so they seldom affect the rendered prompt).
func orderedToolNames(tools ToolSet) []string {
	out := make([]string, 0, len(tools))
	seen := make(map[string]bool, len(tools))
	for _, name := range builtinToolOrder {
		if _, ok := tools[name]; ok {
			out = append(out, name)
			seen[name] = true
		}
	}
	rest := make([]string, 0)
	for name := range tools {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// ToolPromptInfoFor builds the ToolPromptInfo for a tool set by reading each
// tool's optional catools.PromptMetadata (snippet + guidelines).
func ToolPromptInfoFor(tools ToolSet) ToolPromptInfo {
	info := ToolPromptInfo{Snippets: map[string]string{}}
	for _, name := range orderedToolNames(tools) {
		info.OrderedNames = append(info.OrderedNames, name)
		pm, ok := tools[name].(catools.PromptMetadata)
		if !ok {
			continue
		}
		if snippet := pm.PromptSnippet(); snippet != "" {
			info.Snippets[name] = snippet
		}
		info.Guidelines = append(info.Guidelines, pm.PromptGuidelines()...)
	}
	return info
}

func ToolDefinitions(tools ToolSet) []map[string]any {
	return catools.ToolDefinitions(catools.ToolSet(tools))
}

func TruncateHead(content string, maxLines, maxBytes int) catools.TruncationResult {
	return catools.TruncateHead(content, maxLines, maxBytes)
}

func TruncateTail(content string, maxLines, maxBytes int) catools.TruncationResult {
	return catools.TruncateTail(content, maxLines, maxBytes)
}

func FormatSize(bytes int) string {
	return catools.FormatSize(bytes)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
