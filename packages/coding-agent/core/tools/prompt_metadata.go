package tools

// PromptMetadata is an optional capability interface a RuntimeTool may implement
// to contribute to the system prompt: a one-line snippet for the "Available
// tools:" list and guideline bullets for the "Guidelines:" section. It mirrors
// the TS tool `promptSnippet` / `promptGuidelines` fields (see
// ../pi/packages/coding-agent/src/core/tools/*.ts) that buildSystemPrompt
// consumes. A tool appears in the Available tools list only when it returns a
// non-empty PromptSnippet, matching the TS rule (system-prompt.ts:91).
type PromptMetadata interface {
	PromptSnippet() string
	PromptGuidelines() []string
}

// The snippet/guideline strings below are byte-for-byte copies of the upstream
// TS tool definitions so the generated system prompt matches upstream.

// read.ts:213-214
func (ReadTool) PromptSnippet() string { return "Read file contents" }
func (ReadTool) PromptGuidelines() []string {
	return []string{"Use read to examine files instead of cat or sed."}
}

// bash.ts:280 (no guidelines)
func (BashTool) PromptSnippet() string      { return "Execute bash commands (ls, grep, find, etc.)" }
func (BashTool) PromptGuidelines() []string { return nil }

// edit.ts:297-305
func (EditTool) PromptSnippet() string {
	return "Make precise file edits with exact text replacement, including multiple disjoint edits in one call"
}
func (EditTool) PromptGuidelines() []string {
	return []string{
		"Use edit for precise changes (edits[].oldText must match exactly)",
		"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
		"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
		"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
	}
}

// write.ts:191-192
func (WriteTool) PromptSnippet() string { return "Create or overwrite files" }
func (WriteTool) PromptGuidelines() []string {
	return []string{"Use write only for new files or complete rewrites."}
}

// find.ts:118 (no guidelines)
func (FindTool) PromptSnippet() string      { return "Find files by glob pattern (respects .gitignore)" }
func (FindTool) PromptGuidelines() []string { return nil }

// grep.ts:132 (no guidelines)
func (GrepTool) PromptSnippet() string {
	return "Search file contents for patterns (respects .gitignore)"
}
func (GrepTool) PromptGuidelines() []string { return nil }

// ls.ts:104 (no guidelines)
func (LsTool) PromptSnippet() string      { return "List directory contents" }
func (LsTool) PromptGuidelines() []string { return nil }
