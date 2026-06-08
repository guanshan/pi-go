package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
)

func (FindTool) Name() string { return "find" }
func (FindTool) Description() string {
	// Byte-exact with find.ts:117 (DEFAULT_LIMIT=1000, DEFAULT_MAX_BYTES/1024=50).
	return "Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to 1000 results or 50KB (whichever is hit first)."
}
func (FindTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pattern": stringSchema("Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"),
		"path":    stringSchema("Directory to search in (default: current directory)"),
		"limit":   numberSchema("Maximum number of results (default: 1000)"),
	}, []string{"pattern"})
}
func (t FindTool) Execute(ctx context.Context, raw json.RawMessage, _ ToolUpdate) ai.ToolResult {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		// Limit is a pointer so an explicit limit:0 (zero results) is distinguished
		// from an absent limit (DEFAULT_LIMIT), matching find.ts:151 `limit ??
		// DEFAULT_LIMIT`.
		Limit *int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Pattern == "" {
		return toolError("Invalid find input: pattern is required")
	}
	limit := DefaultFindLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	root := ResolveToolPath(t.CWD, firstNonEmpty(args.Path, "."))
	if _, err := os.Stat(root); err != nil {
		return toolError(fmt.Sprintf("Path not found: %s", root))
	}
	if limit <= 0 {
		// An explicit limit:0 yields zero results, matching fd --max-results 0 /
		// the `results.length >= 0` guard in find.ts (effectiveLimit 0).
		return formatFindResults(nil, false, limit, "")
	}
	var results []string
	limitReached := false
	fallbackReason := ""
	if fd := fdFinder(ctx, t.BinDir); fd.Path != "" {
		fdResults, fdLimit, fdErr := searchFd(ctx, fd.Path, root, args.Pattern, limit)
		if fdErr == nil {
			results = fdResults
			limitReached = fdLimit
			return formatFindResults(results, limitReached, limit, "")
		}
		fallbackReason = fmt.Sprintf("fd failed: %v; the built-in filesystem walk was used", fdErr)
	} else if t.BinDir != "" {
		// Match grep's gating: only surface the "fd not found, built-in walk
		// used" notice when an agent bin dir is configured (i.e. fd was expected
		// to be managed). With no bin dir the built-in walk is the normal
		// baseline and the notice is just noise.
		fallbackReason = fdFallbackReason(fd.Diagnostic)
	}
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
	return formatFindResults(results, limitReached, limit, fallbackReason)
}

func formatFindResults(results []string, limitReached bool, limit int, fallbackReason string) ai.ToolResult {
	details := map[string]any{}
	if len(results) == 0 {
		text := "No files found matching pattern"
		if fallbackReason != "" {
			text += "\n\n[" + fallbackReason + "]"
			details["engineFallback"] = fallbackReason
		}
		return ai.ToolResult{Content: ai.TextBlocks(text), Details: detailsOrNil(details)}
	}
	rawOutput := strings.Join(results, "\n")
	trunc := TruncateHead(rawOutput, 1<<30, DefaultMaxBytes)
	text := trunc.Content
	notices := []string{}
	if fallbackReason != "" {
		details["engineFallback"] = fallbackReason
		notices = append(notices, fallbackReason)
	}
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

var fdFinder = func(ctx context.Context, binDir string) managedToolResult {
	return resolveManagedTool(ctx, managedToolFD, binDir)
}

func fdFallbackReason(diagnostic string) string {
	if strings.TrimSpace(diagnostic) != "" {
		return diagnostic + "; the built-in filesystem walk was used"
	}
	return "fd was not found, so the built-in filesystem walk was used"
}

func searchFd(ctx context.Context, fdPath, root, pattern string, limit int) ([]string, bool, error) {
	args := []string{"--hidden", "--glob", "--color=never", "--no-require-git", "--exclude", ".git", "--max-results", strconv.Itoa(limit)}
	// fd --glob matches against the basename unless --full-path is set; in
	// --full-path mode it matches against the full candidate path, so a
	// path-containing pattern like "src/**/*.ts" needs a leading "**/" to match
	// anything (find.ts:236-246).
	effectivePattern := pattern
	if fdGlobNeedsFullPath(pattern) {
		args = append(args, "--full-path")
		// fd's --full-path globs always match against forward-slash paths
		// regardless of OS, so normalize separators before prefixing — a Windows
		// backslash pattern would otherwise become a mixed-separator glob that
		// matches nothing.
		slash := filepath.ToSlash(pattern)
		effectivePattern = slash
		if !strings.HasPrefix(slash, "/") && !strings.HasPrefix(slash, "**/") && slash != "**" {
			effectivePattern = "**/" + slash
		}
	}
	args = append(args, "--", effectivePattern, ".")
	cmd := exec.CommandContext(ctx, fdPath, args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var results []string
	limitReached := false
	killedDueToLimit := false
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" && !limitReached {
			if out := normalizeFdLine(root, line); out != "" {
				results = append(results, out)
				if len(results) >= limit {
					limitReached = true
					killedDueToLimit = true
					_ = cmd.Process.Kill()
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	if waitErr != nil && !killedDueToLimit {
		return nil, false, fmt.Errorf("fd failed: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return results, limitReached, nil
}

func fdGlobNeedsFullPath(pattern string) bool {
	return strings.Contains(filepath.ToSlash(pattern), "/")
}

func normalizeFdLine(root, line string) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	line = strings.TrimPrefix(line, "./")
	if line == "" || line == "." {
		return ""
	}
	hadSlash := strings.HasSuffix(line, "/") || strings.HasSuffix(line, string(os.PathSeparator))
	line = strings.TrimRight(line, `/\`)
	if filepath.IsAbs(line) {
		if rel, err := filepath.Rel(root, line); err == nil {
			line = rel
		}
	}
	out := filepath.ToSlash(line)
	if out == "." || out == "" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(out))); err == nil && info.IsDir() {
		hadSlash = true
	}
	if hadSlash && !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}
