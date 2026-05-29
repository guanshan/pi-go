package ai

import (
	"sort"
)

type ToolSet map[string]Tool

func ToolDefinitions(tools ToolSet) []map[string]any {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tool := tools[name]
		toolName := tool.Name
		if toolName == "" {
			toolName = name
		}
		defs = append(defs, map[string]any{
			"name":        toolName,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		})
	}
	return defs
}

func ToolsByName(tools []Tool) ToolSet {
	if len(tools) == 0 {
		return nil
	}
	out := ToolSet{}
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		out[tool.Name] = tool
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
