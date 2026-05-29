package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

func (LsTool) Name() string { return "ls" }
func (LsTool) Description() string {
	return "List directory contents sorted alphabetically, with / suffix for directories."
}
func (LsTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"path":  stringSchema("Directory to list"),
		"limit": numberSchema("Maximum entries"),
	}, nil)
}
func (t LsTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	_ = json.Unmarshal(raw, &args)
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultLsLimit
	}
	dir := ResolveInCWD(t.CWD, firstNonEmpty(args.Path, "."))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return toolError(err.Error())
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
	out := []string{}
	limitReached := false
	for _, entry := range entries {
		if len(out) >= limit {
			limitReached = true
			break
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return ai.ToolResult{Content: ai.TextBlocks("(empty directory)")}
	}
	rawOutput := strings.Join(out, "\n")
	trunc := TruncateHead(rawOutput, 1<<30, DefaultMaxBytes)
	text := trunc.Content
	details := map[string]any{}
	notices := []string{}
	if limitReached {
		details["entryLimitReached"] = limit
		notices = append(notices, fmt.Sprintf("%d entries limit reached. Use limit=%d for more", limit, limit*2))
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
