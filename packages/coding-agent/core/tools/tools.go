package tools

import (
	"fmt"
	"sort"

	"github.com/guanshan/pi-go/packages/ai"
)

func BuiltinTools(cwd string, options BuiltinToolOptions) ToolSet {
	return ToolSet{
		"read":  ReadTool{CWD: cwd, AutoResize: options.AutoResize, ModelSupportsImages: options.ModelSupportsImages},
		"bash":  BashTool{CWD: cwd, ShellPath: options.ShellPath, CommandPrefix: options.CommandPrefix, BinDir: options.BinDir},
		"edit":  EditTool{CWD: cwd},
		"write": WriteTool{CWD: cwd},
		"grep":  GrepTool{CWD: cwd, BinDir: options.BinDir},
		"find":  FindTool{CWD: cwd, BinDir: options.BinDir},
		"ls":    LsTool{CWD: cwd},
	}
}

func FilterTools(all ToolSet, options FilterOptions) ToolSet {
	if options.NoTools {
		return ToolSet{}
	}
	if len(options.Tools) > 0 {
		out := ToolSet{}
		for _, name := range options.Tools {
			if tool, ok := all[name]; ok {
				out[name] = tool
			}
		}
		return out
	}
	if options.NoBuiltinTools {
		return ToolSet{}
	}
	return ToolSet{
		"read":  all["read"],
		"bash":  all["bash"],
		"edit":  all["edit"],
		"write": all["write"],
	}
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
	return ai.ToolDefinitions(tools.AITools())
}
