package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

func (FindTool) Name() string { return "find" }
func (FindTool) Description() string {
	return "Find files by glob pattern. Respects .gitignore. Output is truncated to 1000 results or 50KB."
}
func (FindTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pattern": stringSchema("Glob pattern, e.g. *.ts or src/**/*.go"),
		"path":    stringSchema("Directory to search"),
		"limit":   numberSchema("Maximum results"),
	}, []string{"pattern"})
}
func (t FindTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Pattern == "" {
		return toolError("Invalid find input: pattern is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultFindLimit
	}
	root := ResolveToolPath(t.CWD, firstNonEmpty(args.Path, "."))
	if _, err := os.Stat(root); err != nil {
		return toolError(fmt.Sprintf("Path not found: %s", root))
	}
	var results []string
	limitReached := false
	// fd lists directories by default and emits results in traversal order (no
	// sort). walkFiltered with includeDirs reports directories too; a matched
	// directory keeps the trailing "/" fd would emit. WalkDir's lexical
	// traversal stands in for fd's traversal order, so we do not re-sort.
	_ = walkFiltered(root, true, func(path string, d os.DirEntry) error {
		if globMatch(args.Pattern, path, root) {
			rel, _ := filepath.Rel(root, path)
			out := filepath.ToSlash(rel)
			if d.IsDir() {
				out += "/"
			}
			results = append(results, out)
			if len(results) >= limit {
				limitReached = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(results) == 0 {
		return ai.ToolResult{Content: ai.TextBlocks("No files found matching pattern")}
	}
	rawOutput := strings.Join(results, "\n")
	trunc := TruncateHead(rawOutput, 1<<30, DefaultMaxBytes)
	text := trunc.Content
	details := map[string]any{}
	notices := []string{}
	if limitReached {
		details["resultLimitReached"] = limit
		notices = append(notices, fmt.Sprintf("%d results limit reached. Use limit=%d for more, or refine pattern", limit, limit*2))
	}
	if trunc.Truncated {
		details["truncation"] = trunc
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
	}
	if len(notices) > 0 {
		text += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: detailsOrNil(details)}
}
