package ai

import "testing"

func TestToolDefinitionsSorted(t *testing.T) {
	defs := ToolDefinitions(ToolSet{
		"z": {Name: "z", Description: "desc z", Parameters: map[string]any{"type": "object"}},
		"a": {Name: "a", Description: "desc a", Parameters: map[string]any{"type": "object"}},
	})
	if len(defs) != 2 || defs[0]["name"] != "a" || defs[1]["name"] != "z" {
		t.Fatalf("defs=%#v", defs)
	}
}
