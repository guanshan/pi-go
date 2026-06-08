package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/guanshan/pi-go/packages/agent/gitignore"
	"github.com/guanshan/pi-go/packages/ai"
)

func (GrepTool) Name() string { return "grep" }
func (GrepTool) Description() string {
	// Byte-exact with grep.ts:131 (DEFAULT_LIMIT=100, DEFAULT_MAX_BYTES/1024=50,
	// GREP_MAX_LINE_LENGTH=500).
	return "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars."
}
func (GrepTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pattern":    stringSchema("Search pattern (regex or literal string)"),
		"path":       stringSchema("Directory or file to search (default: current directory)"),
		"glob":       stringSchema("Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"),
		"ignoreCase": boolSchema("Case-insensitive search (default: false)"),
		"literal":    boolSchema("Treat pattern as literal string instead of regex (default: false)"),
		"context":    numberSchema("Number of lines to show before and after each match (default: 0)"),
		"limit":      numberSchema("Maximum number of matches to return (default: 100)"),
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
		// Limit is a pointer so an explicit limit:0 is distinguished from an
		// absent limit, matching grep.ts:35 Type.Optional(Type.Number()).
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Pattern == "" {
		return toolError("Invalid grep input: pattern is required")
	}
	// Mirror grep.ts:189 effectiveLimit = Math.max(1, limit ?? DEFAULT_LIMIT):
	// absent -> 100, explicit 0 or negative -> clamped to 1.
	limit := DefaultGrepLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if ctx == nil {
		ctx = context.Background()
	}
	root := ResolveToolPath(t.CWD, firstNonEmpty(args.Path, "."))
	info, err := os.Stat(root)
	if err != nil {
		return toolError(fmt.Sprintf("Path not found: %s", root))
	}
	query := grepQuery{
		pattern:    args.Pattern,
		glob:       args.Glob,
		ignoreCase: args.IgnoreCase,
		literal:    args.Literal,
		context:    args.Context,
		limit:      limit,
	}

	var (
		results        []string
		matchLimit     bool
		linesTruncated bool
		fallbackReason string
	)
	// Prefer ripgrep (the engine the TypeScript grep tool shells out to) so
	// look-around/backreference patterns behave identically. Fall back to Go's
	// RE2 engine only when no rg binary is found.
	if rg := ripgrepFinder(ctx, t.BinDir); rg.Path != "" {
		res, ml, lt, rerr := searchRipgrep(ctx, rg.Path, root, info.IsDir(), query)
		if rerr != nil {
			return toolError(rerr.Error())
		}
		results, matchLimit, linesTruncated = res, ml, lt
	} else {
		if t.BinDir != "" {
			fallbackReason = rgFallbackReason(rg.Diagnostic)
		}
		pattern := args.Pattern
		if args.Literal {
			pattern = regexp.QuoteMeta(pattern)
		}
		if args.IgnoreCase {
			pattern = "(?i)" + pattern
		}
		// Without ripgrep we use Go's regexp package (RE2). RE2 and ripgrep's
		// default Rust regex engine have the same feature set here (neither supports
		// look-around or backreferences), but their accept/reject edges and ignore
		// semantics differ subtly. When rg was expected from a managed bin dir,
		// surface the fallback in compile errors so behavior is explainable.
		re, cerr := regexp.Compile(pattern)
		if cerr != nil {
			if fallbackReason != "" {
				return toolError(fmt.Sprintf("%s\n(%s)", cerr.Error(), fallbackReason))
			}
			return toolError(cerr.Error())
		}
		results, matchLimit, linesTruncated = searchRE2(re, root, info.IsDir(), query)
	}
	if len(results) == 0 {
		text := "No matches found"
		details := map[string]any{}
		if fallbackReason != "" {
			text += "\n\n[" + fallbackReason + "]"
			details["engineFallback"] = fallbackReason
		}
		return ai.ToolResult{Content: ai.TextBlocks(text), Details: detailsOrNil(details)}
	}
	rawOutput := strings.Join(results, "\n")
	trunc := TruncateHead(rawOutput, 1<<30, DefaultMaxBytes)
	text := trunc.Content
	details := map[string]any{}
	notices := []string{}
	if fallbackReason != "" {
		details["engineFallback"] = fallbackReason
		notices = append(notices, fallbackReason)
	}
	if matchLimit {
		details["matchLimitReached"] = limit
		notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", limit, limit*2))
	}
	if trunc.Truncated {
		details["truncation"] = trunc
		notices = append(notices, fmt.Sprintf("%s limit reached", FormatSize(DefaultMaxBytes)))
	}
	if linesTruncated {
		details["linesTruncated"] = true
		notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use read tool to see full lines", GrepMaxLineLength))
	}
	if len(notices) > 0 {
		text += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	return ai.ToolResult{Content: ai.TextBlocks(text), Details: detailsOrNil(details)}
}

// grepQuery carries the resolved grep arguments shared by the ripgrep and RE2
// search paths.
type grepQuery struct {
	pattern    string
	glob       string
	ignoreCase bool
	literal    bool
	context    int
	limit      int
}

// ripgrepFinder resolves the rg binary; it is a package var so tests can force
// the RE2 fallback path.
var ripgrepFinder = func(ctx context.Context, binDir string) managedToolResult {
	return resolveManagedTool(ctx, managedToolRG, binDir)
}

func rgFallbackReason(diagnostic string) string {
	if strings.TrimSpace(diagnostic) != "" {
		return diagnostic + "; the built-in RE2 engine was used"
	}
	return "ripgrep was not found, so the built-in RE2 engine was used; install ripgrep to match the TypeScript CLI's regex engine"
}

// rgEvent is the subset of ripgrep's --json event stream we consume.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
	} `json:"data"`
}

// searchRipgrep runs ripgrep with the same flags as the TypeScript grep tool
// (grep.ts ~215) and formats its matches identically. .git is always excluded so
// git internals never surface, matching the RE2 fallback (walkFiltered). The
// match-line/context-line formatting mirrors searchRE2.
func searchRipgrep(ctx context.Context, rgPath, root string, rootIsDir bool, q grepQuery) (results []string, matchLimit, linesTruncated bool, err error) {
	rgArgs := []string{"--json", "--line-number", "--color=never", "--hidden", "--glob", "!.git"}
	if q.ignoreCase {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if q.literal {
		rgArgs = append(rgArgs, "--fixed-strings")
	}
	if q.glob != "" {
		rgArgs = append(rgArgs, "--glob", q.glob)
	}
	rgArgs = append(rgArgs, "--", q.pattern, root)

	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, perr := cmd.StdoutPipe()
	if perr != nil {
		return nil, false, false, perr
	}
	if serr := cmd.Start(); serr != nil {
		return nil, false, false, serr
	}

	type rgMatch struct {
		path string
		line int
		text string
	}
	var matches []rgMatch
	killedDueToLimit := false
	reader := bufio.NewReader(stdout)
	for {
		line, rerr := reader.ReadBytes('\n')
		// Keep draining after the limit is hit so the killed process's pipe
		// closes cleanly; only stop collecting matches.
		if len(line) > 0 && !matchLimit {
			var ev rgEvent
			if json.Unmarshal(bytes.TrimSpace(line), &ev) == nil && ev.Type == "match" {
				if ev.Data.Path.Text != "" && ev.Data.LineNumber > 0 {
					matches = append(matches, rgMatch{path: ev.Data.Path.Text, line: ev.Data.LineNumber, text: ev.Data.Lines.Text})
				}
				if len(matches) >= q.limit {
					matchLimit = true
					killedDueToLimit = true
					_ = cmd.Process.Kill()
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, false, false, ctx.Err()
	}
	if !killedDueToLimit && werr != nil {
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) {
			// ripgrep exits 1 when there are simply no matches; that is not an error.
			if exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = fmt.Sprintf("ripgrep exited with code %d", exitErr.ExitCode())
				}
				return nil, false, false, errors.New(msg)
			}
		} else {
			return nil, false, false, werr
		}
	}

	fileLines := map[string][]string{}
	getLines := func(path string) []string {
		if cached, ok := fileLines[path]; ok {
			return cached
		}
		var lines []string
		if data, e := os.ReadFile(path); e == nil {
			normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
			normalized = strings.ReplaceAll(normalized, "\r", "\n")
			lines = strings.Split(normalized, "\n")
		}
		fileLines[path] = lines
		return lines
	}
	for _, m := range matches {
		prefix := formatRel(m.path, root, rootIsDir)
		if q.context <= 0 {
			text := strings.ReplaceAll(m.text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "")
			text = strings.TrimSuffix(text, "\n")
			truncated, was := truncateLine(text, GrepMaxLineLength)
			if was {
				linesTruncated = true
			}
			results = append(results, fmt.Sprintf("%s:%d: %s", prefix, m.line, truncated))
			continue
		}
		lines := getLines(m.path)
		if len(lines) == 0 {
			results = append(results, fmt.Sprintf("%s:%d: (unable to read file)", prefix, m.line))
			continue
		}
		start := max(1, m.line-q.context)
		end := min(len(lines), m.line+q.context)
		for cur := start; cur <= end; cur++ {
			lineText := strings.ReplaceAll(lines[cur-1], "\r", "")
			truncated, was := truncateLine(lineText, GrepMaxLineLength)
			if was {
				linesTruncated = true
			}
			// MATCH lines use "path:N: text" (colon + space); CONTEXT lines use
			// "path-N- text" (dash separators), mirroring ripgrep's grep output.
			if cur == m.line {
				results = append(results, fmt.Sprintf("%s:%d: %s", prefix, cur, truncated))
			} else {
				results = append(results, fmt.Sprintf("%s-%d- %s", prefix, cur, truncated))
			}
		}
	}
	return results, matchLimit, linesTruncated, nil
}

// searchRE2 is the ripgrep-free fallback: it walks the tree honoring
// .gitignore/.ignore (and skipping .git) and matches each line with Go's RE2
// engine, producing output byte-identical to the ripgrep path.
func searchRE2(re *regexp.Regexp, root string, rootIsDir bool, q grepQuery) (results []string, matchLimit, linesTruncated bool) {
	searchFile := func(path string) error {
		if len(results) >= q.limit {
			matchLimit = true
			return filepath.SkipAll
		}
		if q.glob != "" && !globMatch(q.glob, path, root) {
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
			start := max(0, i-q.context)
			end := min(len(lines)-1, i+q.context)
			for n := start; n <= end; n++ {
				prefix := formatRel(path, root, rootIsDir)
				text, wasTruncated := truncateLine(lines[n], GrepMaxLineLength)
				if wasTruncated {
					linesTruncated = true
				}
				if n == i {
					results = append(results, fmt.Sprintf("%s:%d: %s", prefix, n+1, text))
				} else {
					results = append(results, fmt.Sprintf("%s-%d- %s", prefix, n+1, text))
				}
			}
			if len(results) >= q.limit {
				matchLimit = true
				return filepath.SkipAll
			}
		}
		return nil
	}
	if !rootIsDir {
		_ = searchFile(root)
	} else {
		_ = walkFiltered(root, false, func(path string, _ os.DirEntry) error {
			return searchFile(path)
		})
	}
	return results, matchLimit, linesTruncated
}

// searchIgnoreFileNames are the ignore files honored when walking a directory,
// matching the set rg/fd consult by default.
var searchIgnoreFileNames = []string{".gitignore", ".ignore"}

// walkFiltered walks root and invokes fn for every file that is not ignored. It
// always skips .git and honors .gitignore/.ignore rules hierarchically (loading
// each directory's ignore files as it descends), mirroring the rg/fd semantics
// the TypeScript grep/find tools rely on. Hidden files are included; only ignore
// rules (and .git) prune the tree. fn may return filepath.SkipAll/SkipDir.
//
// When includeDirs is true, fn is also invoked for each directory that is not
// pruned (root itself is never reported). fd lists directories by default, so
// the find tool sets this; grep (ripgrep, file contents only) leaves it false.
func walkFiltered(root string, includeDirs bool, fn func(path string, d os.DirEntry) error) error {
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
			if includeDirs && path != root {
				return fn(path, d)
			}
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
		// Mirror grep.ts formatPath: only use the relative path when it stays
		// inside the search dir (does not start with ".."); otherwise fall back to
		// the bare basename. This matters for matches that resolve outside root
		// (e.g. via a symlink).
		if rel, err := filepath.Rel(root, path); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(path)
}

// truncateLine truncates a single line to maxChars UTF-16 code units, appending
// the "... [truncated]" suffix (matching truncate.ts truncateLine, which uses
// JavaScript string .length / .slice — UTF-16 code units) and reporting whether
// truncation happened so callers can surface a notice.
func truncateLine(line string, maxChars int) (string, bool) {
	units := utf16Units(line)
	if len(units) <= maxChars {
		return line, false
	}
	return utf16Slice(units, 0, maxChars) + "... [truncated]", true
}

// utf16Units returns the UTF-16 code units of s, matching the semantics of a
// JavaScript string (where .length counts UTF-16 code units and non-BMP
// characters occupy two units).
func utf16Units(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

// utf16Slice decodes units[start:end] back to a UTF-8 string, mirroring
// JavaScript String.prototype.slice over UTF-16 code units.
func utf16Slice(units []uint16, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(units) {
		end = len(units)
	}
	if start >= end {
		return ""
	}
	return string(utf16.Decode(units[start:end]))
}
