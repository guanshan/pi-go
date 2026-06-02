package core

import (
	"fmt"
	"sort"

	"github.com/guanshan/pi-go/packages/coding-agent/cli"
	catools "github.com/guanshan/pi-go/packages/coding-agent/core/tools"
)

const DefaultAgentMaxLoop = 25

type ToolSet map[string]catools.RuntimeTool

func BuiltinTools(cwd string, settings *SettingsManager) ToolSet {
	options := catools.BuiltinToolOptions{AutoResize: true, BinDir: BinDir()}
	if settings != nil {
		options.ShellPath = settings.mergedString(settings.Global.ShellPath, settings.Project.ShellPath, "")
		options.CommandPrefix = settings.ShellCommandPrefix()
		options.AutoResize = settings.ImageAutoResize()
	}
	return ToolSet(catools.BuiltinTools(cwd, options))
}

func FilterTools(all ToolSet, args cli.Args) ToolSet {
	return ToolSet(catools.FilterTools(catools.ToolSet(all), catools.FilterOptions{
		NoTools:        args.NoTools,
		Tools:          args.Tools,
		NoBuiltinTools: args.NoBuiltinTools,
	}))
}

func AllToolDescriptions(tools ToolSet) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%s: %s", name, tools[name].Description()))
	}
	return out
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
