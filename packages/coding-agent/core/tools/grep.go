package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/guanshan/pi-go/packages/agent/gitignore"
	"github.com/guanshan/pi-go/packages/ai"
)

func (GrepTool) Name() string { return "grep" }
func (GrepTool) Description() string {
	return "Search file contents for a regex or literal pattern. Respects .gitignore."
}
func (GrepTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pattern":    stringSchema("Search pattern"),
		"path":       stringSchema("Directory or file to search"),
		"glob":       stringSchema("Glob filter, e.g. *.ts or **/*.spec.ts"),
		"ignoreCase": boolSchema("Case-insensitive search"),
		"literal":    boolSchema("Treat pattern as a literal string"),
		"context":    numberSchema("Context lines before and after"),
		"limit":      numberSchema("Maximum matches"),
	}, []string{"pattern"})
}
func (t GrepTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		IgnoreCase bool   `json:"ignoreCase"`
		Literal    bool   `json:"literal"`
		Context    int    `json:"context"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Pattern == "" {
		return toolError("Invalid grep input: pattern is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultGrepLimit
	}
	root := ResolveInCWD(t.CWD, firstNonEmpty(args.Path, "."))
	info, err := os.Stat(root)
	if err != nil {
		return toolError(fmt.Sprintf("Path not found: %s", root))
	}
	pattern := args.Pattern
	if args.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if args.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return toolError(err.Error())
	}
	var results []string
	matchLimit := false
	searchFile := func(path string) error {
		if len(results) >= limit {
			matchLimit = true
			return filepath.SkipAll
		}
		if args.Glob != "" && !globMatch(args.Glob, path, root) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			start := max(0, i-args.Context)
			end := min(len(lines)-1, i+args.Context)
			for n := start; n <= end; n++ {
				prefix := formatRel(path, root, info.IsDir())
				text := truncateLine(lines[n], GrepMaxLineLength)
				results = append(results, fmt.Sprintf("%s:%d:%s", prefix, n+1, text))
			}
			if len(results) >= limit {
				matchLimit = true
				return filepath.SkipAll
			}
		}
		return nil
	}
	if !info.IsDir() {
		_ = searchFile(root)
	} else {
		_ = walkFiltered(root, func(path string, _ os.DirEntry) error {
			return searchFile(path)
		})
	}
	if len(results) == 0 {
		return ai.ToolResult{Content: ai.TextBlocks("No matches found")}
	}
	rawOutput := strings.Join(results, "\n")
	trunc := TruncateHead(rawOutput, 1<<30, DefaultMaxBytes)
	text := trunc.Content
	details := map[string]any{}
	notices := []string{}
	if matchLimit {
		details["matchLimitReached"] = limit
		notices = append(notices, fmt.Sprintf("%d matches limit", limit))
	}
	if trunc.Truncated {
		details["truncation"] = trunc
		notices = append(notices, fmt.Sprintf("%s limit", FormatSize(DefaultMaxBytes)))
	}
	if len(notices) > 0 {
		text += "\n\n[Truncated: " + strings.Join(notices, ", ") + "]"
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: detailsOrNil(details)}
}

// searchIgnoreFileNames are the ignore files honored when walking a directory,
// matching the set rg/fd consult by default.
var searchIgnoreFileNames = []string{".gitignore", ".ignore"}

// walkFiltered walks root and invokes fn for every file that is not ignored. It
// always skips .git and honors .gitignore/.ignore rules hierarchically (loading
// each directory's ignore files as it descends), mirroring the rg/fd semantics
// the TypeScript grep/find tools rely on. Hidden files are included; only ignore
// rules (and .git) prune the tree. fn may return filepath.SkipAll/SkipDir.
func walkFiltered(root string, fn func(path string, d os.DirEntry) error) error {
	matcher := gitignore.New()
	loadDirIgnores := func(dir, rel string) {
		prefix := ""
		if rel != "" {
			prefix = rel + "/"
		}
		for _, name := range searchIgnoreFileNames {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
				if pattern := gitignore.PrefixPattern(line, prefix); pattern != "" {
					matcher.Add(pattern)
				}
			}
		}
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = ""
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if d.IsDir() {
			if path != root {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				if matcher.Ignores(rel + "/") {
					return filepath.SkipDir
				}
			}
			// A directory's own ignore files apply to its contents, so load them
			// after deciding whether the directory itself is pruned.
			loadDirIgnores(path, rel)
			return nil
		}
		if rel != "" && matcher.Ignores(rel) {
			return nil
		}
		return fn(path, d)
	})
}

func globMatch(pattern, path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	pattern = filepath.ToSlash(pattern)
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		if ok, _ := filepath.Match(pattern, filepath.Base(rel)); ok {
			return true
		}
	}
	re := globToRegexp(pattern)
	return re.MatchString(rel)
}

func globToRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func formatRel(path, root string, rootIsDir bool) string {
	if rootIsDir {
		if rel, err := filepath.Rel(root, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(path)
}

func truncateLine(line string, maxChars int) string {
	r := []rune(line)
	if len(r) <= maxChars {
		return line
	}
	return string(r[:maxChars]) + "..."
}
