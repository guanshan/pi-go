package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

func (LsTool) Name() string { return "ls" }
func (LsTool) Description() string {
	// Byte-exact with ls.ts:103 (DEFAULT_LIMIT=500, DEFAULT_MAX_BYTES/1024=50).
	return "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or 50KB (whichever is hit first)."
}
func (LsTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"path":  stringSchema("Directory to list (default: current directory)"),
		"limit": numberSchema("Maximum number of entries to return (default: 500)"),
	}, nil)
}
func (t LsTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Path string `json:"path"`
		// Limit is a pointer so an explicit limit:0 (empty listing) is
		// distinguished from an absent limit (DEFAULT_LIMIT), matching ls.ts:125
		// `limit ?? DEFAULT_LIMIT`.
		Limit *int `json:"limit"`
	}
	_ = json.Unmarshal(raw, &args)
	limit := DefaultLsLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	dir := ResolveToolPath(t.CWD, firstNonEmpty(args.Path, "."))
	// Mirror TS ls.ts:127-147: distinguish "not found", "not a directory",
	// and "cannot read directory" so the model sees consistent wording.
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError(fmt.Sprintf("Path not found: %s", dir))
		}
		return toolError(err.Error())
	}
	if !info.IsDir() {
		return toolError(fmt.Sprintf("Not a directory: %s", dir))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return toolError(fmt.Sprintf("Cannot read directory: %s", err.Error()))
	}
	// Match TS ls.ts:150 `a.toLowerCase().localeCompare(b.toLowerCase())`: a
	// case-insensitive locale-aware order so non-ASCII filenames sort like TS
	// instead of by raw byte value.
	sort.SliceStable(entries, func(i, j int) bool {
		return localeCompareLower(entries[i].Name(), entries[j].Name()) < 0
	})
	out := []string{}
	limitReached := false
	for _, entry := range entries {
		if len(out) >= limit {
			limitReached = true
			break
		}
		name := entry.Name()
		// Follow symlinks when deciding the trailing slash, matching TS which
		// stats the full path (ls.ts:159-166) rather than using the dirent type.
		// TS skips entries it cannot stat (broken symlinks, unreadable entries);
		// see ls.ts:166-168 `catch { continue }`.
		info, statErr := os.Stat(filepath.Join(dir, entry.Name()))
		if statErr != nil {
			continue
		}
		if info.IsDir() {
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
