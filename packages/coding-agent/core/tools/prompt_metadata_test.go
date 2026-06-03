package tools

import (
	"reflect"
	"testing"
)

// TestToolPromptMetadataValues asserts each builtin tool exposes the exact
// upstream TS promptSnippet / promptGuidelines values via PromptMetadata, and
// that all seven builtins implement the interface.
func TestToolPromptMetadataValues(t *testing.T) {
	cases := []struct {
		name       string
		tool       RuntimeTool
		snippet    string
		guidelines []string
	}{
		{"read", ReadTool{}, "Read file contents", []string{"Use read to examine files instead of cat or sed."}},
		{"bash", BashTool{}, "Execute bash commands (ls, grep, find, etc.)", nil},
		{"edit", EditTool{}, "Make precise file edits with exact text replacement, including multiple disjoint edits in one call", []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		}},
		{"write", WriteTool{}, "Create or overwrite files", []string{"Use write only for new files or complete rewrites."}},
		{"find", FindTool{}, "Find files by glob pattern (respects .gitignore)", nil},
		{"grep", GrepTool{}, "Search file contents for patterns (respects .gitignore)", nil},
		{"ls", LsTool{}, "List directory contents", nil},
	}
	for _, tc := range cases {
		pm, ok := tc.tool.(PromptMetadata)
		if !ok {
			t.Fatalf("%s tool does not implement PromptMetadata", tc.name)
		}
		if got := pm.PromptSnippet(); got != tc.snippet {
			t.Errorf("%s snippet=%q want %q", tc.name, got, tc.snippet)
		}
		if got := pm.PromptGuidelines(); !reflect.DeepEqual(got, tc.guidelines) {
			t.Errorf("%s guidelines=%#v want %#v", tc.name, got, tc.guidelines)
		}
	}
}
