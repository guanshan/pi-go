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
	"runtime"
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
	if fdPath, ok := fdFinder(t.BinDir); ok {
		fdResults, fdLimit, fdErr := searchFd(ctx, fdPath, root, args.Pattern, limit)
		if fdErr == nil {
			results = fdResults
			limitReached = fdLimit
			return formatFindResults(results, limitReached, limit)
		}
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
	return formatFindResults(results, limitReached, limit)
}

func formatFindResults(results []string, limitReached bool, limit int) ai.ToolResult {
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

var fdFinder = findFd

func findFd(binDir string) (string, bool) {
	names := []string{"fd"}
	if runtime.GOOS == "windows" {
		names = []string{"fd.exe"}
	}
	if binDir != "" {
		for _, name := range names {
			candidate := filepath.Join(binDir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, true
			}
		}
	}
	for _, name := range append(names, "fdfind") {
		if path, err := exec.LookPath(name); err == nil && path != "" {
			return path, true
		}
	}
	return "", false
}

func searchFd(ctx context.Context, fdPath, root, pattern string, limit int) ([]string, bool, error) {
	args := []string{"--hidden", "--glob", "--color=never", "--no-require-git", "--exclude", ".git"}
	if fdGlobNeedsFullPath(pattern) {
		args = append(args, "--full-path")
	}
	args = append(args, "--", pattern, ".")
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
