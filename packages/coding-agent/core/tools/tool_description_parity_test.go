package tools

import "testing"

// TestToolDescriptionsMatchTS pins each built-in tool's model-facing Description()
// to the byte-exact TS string (read.ts:212, write.ts:189, edit.ts:295,
// bash.ts:279, grep.ts:131, find.ts:117, ls.ts:103). These strings are sent to
// the model, so any drift from the TS catalog is a parity regression.
func TestToolDescriptionsMatchTS(t *testing.T) {
	cases := []struct {
		tool RuntimeTool
		want string
	}{
		{ReadTool{}, "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete."},
		{WriteTool{}, "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories."},
		{EditTool{}, "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes."},
		{BashTool{}, "Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last 2000 lines or 50KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds."},
		{GrepTool{}, "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars."},
		{FindTool{}, "Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to 1000 results or 50KB (whichever is hit first)."},
		{LsTool{}, "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or 50KB (whichever is hit first)."},
	}
	for _, c := range cases {
		if got := c.tool.Description(); got != c.want {
			t.Errorf("%s description drift:\n got=%q\nwant=%q", c.tool.Name(), got, c.want)
		}
	}
}

// TestToolSchemaDescriptionsMatchTS pins the per-parameter schema descriptions to
// the TS typebox schemas. These are also sent to the model.
func TestToolSchemaDescriptionsMatchTS(t *testing.T) {
	check := func(t *testing.T, name string, schema map[string]any, prop, want string) {
		t.Helper()
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: schema has no properties", name)
		}
		field, ok := props[prop].(map[string]any)
		if !ok {
			t.Fatalf("%s: missing property %q", name, prop)
		}
		if got, _ := field["description"].(string); got != want {
			t.Errorf("%s.%s description drift:\n got=%q\nwant=%q", name, prop, got, want)
		}
	}

	readSchema := ReadTool{}.Schema()
	check(t, "read", readSchema, "path", "Path to the file to read (relative or absolute)")
	check(t, "read", readSchema, "offset", "Line number to start reading from (1-indexed)")
	check(t, "read", readSchema, "limit", "Maximum number of lines to read")

	writeSchema := WriteTool{}.Schema()
	check(t, "write", writeSchema, "path", "Path to the file to write (relative or absolute)")
	check(t, "write", writeSchema, "content", "Content to write to the file")

	editSchema := EditTool{}.Schema()
	check(t, "edit", editSchema, "path", "Path to the file to edit (relative or absolute)")
	if edits, ok := editSchema["properties"].(map[string]any)["edits"].(map[string]any); ok {
		if got, _ := edits["description"].(string); got != "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead." {
			t.Errorf("edit.edits description drift: %q", got)
		}
		if items, ok := edits["items"].(map[string]any); ok {
			check(t, "edit.items", items, "oldText", "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call.")
			check(t, "edit.items", items, "newText", "Replacement text for this targeted edit.")
		} else {
			t.Errorf("edit.edits has no items schema")
		}
	} else {
		t.Errorf("edit schema missing edits array")
	}

	bashSchema := BashTool{}.Schema()
	check(t, "bash", bashSchema, "command", "Bash command to execute")
	check(t, "bash", bashSchema, "timeout", "Timeout in seconds (optional, no default timeout)")

	grepSchema := GrepTool{}.Schema()
	check(t, "grep", grepSchema, "pattern", "Search pattern (regex or literal string)")
	check(t, "grep", grepSchema, "path", "Directory or file to search (default: current directory)")
	check(t, "grep", grepSchema, "glob", "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'")
	check(t, "grep", grepSchema, "ignoreCase", "Case-insensitive search (default: false)")
	check(t, "grep", grepSchema, "literal", "Treat pattern as literal string instead of regex (default: false)")
	check(t, "grep", grepSchema, "context", "Number of lines to show before and after each match (default: 0)")
	check(t, "grep", grepSchema, "limit", "Maximum number of matches to return (default: 100)")

	findSchema := FindTool{}.Schema()
	check(t, "find", findSchema, "pattern", "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'")
	check(t, "find", findSchema, "path", "Directory to search in (default: current directory)")
	check(t, "find", findSchema, "limit", "Maximum number of results (default: 1000)")

	lsSchema := LsTool{}.Schema()
	check(t, "ls", lsSchema, "path", "Directory to list (default: current directory)")
	check(t, "ls", lsSchema, "limit", "Maximum number of entries to return (default: 500)")
}
