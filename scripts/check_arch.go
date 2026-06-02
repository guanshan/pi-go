//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const modulePath = "github.com/guanshan/pi-go"

const (
	defaultMaxPackageFiles = 15
	defaultMaxPackageLines = 3000
	defaultMaxFileLines    = 1000
)

type packageLimit struct {
	MaxFiles int
	MaxLines int
	Reason   string
}

type packageStats struct {
	Files  int
	Lines  int
	HasDoc bool
}

var temporaryPackageLimits = map[string]packageLimit{
	"packages/agent/harness":           {MaxFiles: 16, MaxLines: 3781, Reason: "post-parity-fix ratcheted budget; includes P1-C1 emitRunFailure run-failure termination sequence in harness.go; awaits full prompt/session/hook subpackage extraction; + review-parity round: array-content custom-message reload normalization + JSONL SetEscapeHTML(false) write parity"},
	"packages/ai":                      {MaxFiles: 34, MaxLines: 8481, Reason: "post-parity-fix ratcheted budget; includes existing credential-dir chmod tightening; remaining oauth/model catalog/root provider adapters, programmable faux provider, retry classification (IsRetryableProviderError), strict cross-provider handoff same-model check (transform_messages), and P2 wrap-up (OAuth identity cache_control, validation casing, azure prompt_cache_key gate, cloudflare punctuation, 2-space auth.json) plus session summary text compatibility await final package split; + review-parity round: stream finish_reason truncation guard, Bedrock reserved-header skip, OpenRouter developer-role + tool-result name; + model-catalog regen: OpenAICompat/AnthropicMessagesCompat supportsTemperature field + strPtr helper for generated thinkingLevelMap"},
	"packages/ai/providers":            {MaxFiles: 15, MaxLines: 5521, Reason: "post-parity-fix ratcheted budget; provider protocol implementations migrated from packages/ai root; split by provider family if this grows further; + review-parity round: SetEscapeHTML(false) MarshalJSON/UnescapeJSONHTML + streaming cache-write/DeepSeek-cache-hit usage; + model-catalog regen: AnthropicRequestOptions.SupportsTemperature gate"},
	"packages/coding-agent":            {MaxFiles: 21, MaxLines: 5656, Reason: "post-parity-fix ratcheted package-manager TS install-layout (git/<host>/<owner>/<repo>, npm/node_modules/<name>) + real npm install + legacy fallback budget plus rollback/dependency and platform-split signal shutdown (signal_unix/windows.go); awaits resource-loader and package-manager/config split"},
	"packages/coding-agent/core":       {MaxFiles: 29, MaxLines: 12460, Reason: "post-parity-fix ratcheted P0/P1 session/runtime parity budget plus session-info parity, uncapped JSONL line reader (P0-4), ThinkingBudgets getter + SessionID/Transport/ThinkingBudgets/MaxRetryDelayMs AgentOptions wiring (P1-A1), print-mode text output parity and exit-code cleanup (P1-A3: final-assistant-text-only on stdout, error/aborted to stderr + exit 1), BinDir() + bash shell-env PATH injection wiring (P1-G1), P2 context-file discovery parity (4 casings, first-per-dir, global->root->cwd order, no re-sort), P1-5 interactive cross-project fork + missing-cwd continue prompts (cli.Confirm) with session cwdOverride, P2-1 system-prompt project-context/skills XML shapes, and extension tool_result mutated-input replay; awaits runtime/session/modes subpackage split; + review-parity round: default model/thinking persistence, enabledModels cycle scope, RPC bash sanitize/truncate + RPC SetEscapeHTML(false), ext ui.confirm/select errors + register* graceful-degrade, pi-update npm reinstall; + interactive-TUI slice 1: Ctrl+P/Shift+Ctrl+P model cycling (CycleModelBackward + cycleModel handler); + interactive-TUI slice 2: /model selector overlay (interactive_model_selector.go SelectList-backed navigable picker, Ctrl+L + bare /model entry)"},
	"packages/coding-agent/core/tools": {MaxFiles: 26, MaxLines: 2957, Reason: "ratcheted budget; one file per tool plus platform-split exec/replace files (bash_exec_unix/windows/other, file_replace_*), edit fuzzy NFKC/error-message + read/ls wording parity helpers, ShellEnv() PATH-injection helper (P1-G1), shared FileURLToPath parser (fileurl.go) reused by tools/path.go, NormalizePath, and the CLI file processor (P2-3 file:// unification), ripgrep-preferring grep with RE2 fallback (P2-2), detached-child PID registry/process-group liveness checks (detached.go + killProcessTreeByPID, P2-3), and multi-key file mutation queue locking for Windows-safe create/overwrite races; split if tool bodies grow; + review-parity round: bash_executor.go (sanitize/truncate mirror of bash-executor.ts), ls skip-unstatable"},
	"packages/tui":                     {MaxFiles: 30, MaxLines: 6460, Reason: "ratcheted TUI primitive budget plus emoji-width compatibility; revisit with subpackages if this grows"},
}

var temporaryFileLineLimits = map[string]packageLimit{
	"packages/agent/harness/harness.go":             {MaxLines: 1030, Reason: "ratcheted for P1-C1 emitRunFailure/createFailureMessage run-failure termination sequence; awaits harness run-loop extraction"},
	"packages/coding-agent/core/interactive_tui.go": {MaxLines: 1200, Reason: "ratcheted for P1-4 per-command child-context cancellation (beginCommand/clearCommandCancel + Escape handling so a running slash/bash command can be aborted, not just an agent turn) + interactive-TUI slice 1 Ctrl+P/Shift+Ctrl+P model cycling (key cases + cycleModel handler) + interactive-TUI slice 2 /model selector overlay wiring (modelSelector field, key routing, Ctrl+L + bare-/model entry, View input-region swap, openModelSelector/handleModelSelectorKey/applyModelSelection); overlay struct/render/key logic lives in interactive_model_selector.go; awaits further selector/overlay extraction"},
}

// wiredTUIComponents is the explicit allowlist of exported packages/tui symbols
// that production code (cmd/ + packages/coding-agent, excluding tests) is allowed
// to consume. packages/tui is a ~9500-line component library that, per the parity
// review (P1-F1, topic 5), is largely "ported but not wired" under route A: only a
// handful of symbols sit on a live production path. The lightweight check below
// asserts that every tui.<Symbol> referenced by production code appears here, so
// that newly wiring an additional component is a deliberate, recorded act rather
// than silent dead-code activation.
//
// TODO(P1-F1): this is the lightweight half of the requested arch check. It does
// not yet assert the inverse direction (every exported tui symbol either has a
// production consumer or is explicitly marked not-wired); a full static
// reachability check over ~215 exported declarations was judged too heavy for this
// pass. See docs/TS_COMPATIBILITY.md (packages/tui section) for the full
// ported/wired/not-wired classification.
var wiredTUIComponents = map[string]bool{
	"TruncateToWidth":  true,
	"VisibleWidth":     true,
	"NewMarkdown":      true,
	"MarkdownTheme":    true,
	"FuzzyMatchString": true,
	// interactive-TUI slice 2: the /model selector overlay
	// (interactive_model_selector.go) drives the SelectList component for
	// navigable model picking.
	"NewSelectList":           true,
	"SelectList":              true,
	"SelectItem":              true,
	"SelectListTheme":         true,
	"SelectListLayoutOptions": true,
}

func main() {
	var failures []string
	stats := map[string]*packageStats{}
	var hasAgentPackageFiles bool
	var hasTUIPackageFiles bool
	var codingAgentImportsAgent bool
	var codingAgentImportsTUI bool
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".workspace", "dist", "bin", "node_modules":
				return filepath.SkipDir
			}
			if entry.Name() == "internal" {
				failures = append(failures, "root internal/ directory is not allowed in the target architecture")
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		path = filepath.ToSlash(path)
		dir := filepath.ToSlash(filepath.Dir(path))
		packageStat := stats[dir]
		if packageStat == nil {
			packageStat = &packageStats{}
			stats[dir] = packageStat
		}
		packageStat.Files++
		if entry.Name() == "doc.go" {
			packageStat.HasDoc = true
		}
		lines, generated, err := fileLineStats(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if !generated {
			packageStat.Lines += lines
			limit := fileLineLimitFor(path)
			if lines > limit.MaxLines {
				failures = append(failures, fmt.Sprintf("%s: file has %d lines; limit is %d lines (%s)",
					path, lines, limit.MaxLines, limit.Reason))
			}
		}
		if strings.HasPrefix(path, "packages/agent/") {
			hasAgentPackageFiles = true
		}
		if strings.HasPrefix(path, "packages/tui/") {
			hasTUIPackageFiles = true
		}
		imports, err := fileImports(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if strings.HasPrefix(path, "packages/coding-agent/") {
			// P6: packages/coding-agent must not declare type aliases. This means
			// the public coding-agent package cannot transparently re-export
			// core/coreext types behind a facade, so its signatures expose those
			// implementation types directly (parity review P1-F2, topic 6).
			// DECISION (recorded in docs/TS_COMPATIBILITY.md, packages/coding-agent
			// section): keep P6 as-is and treat core + core/extensions as stable
			// public sub-APIs, rather than relaxing P6 for a single-package
			// re-export facade. Do not loosen this rule without updating that doc.
			aliases, err := fileTypeAliases(path)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			for _, name := range aliases {
				failures = append(failures, fmt.Sprintf("%s: type alias %s violates target architecture P6", path, name))
			}
		}
		for _, importPath := range imports {
			failures = append(failures, checkImport(path, importPath)...)
			relImport := strings.TrimPrefix(importPath, modulePath+"/")
			if strings.HasPrefix(path, "packages/coding-agent/") {
				if importsAny(relImport, "packages/agent") {
					codingAgentImportsAgent = true
				}
				if importsAny(relImport, "packages/tui") {
					codingAgentImportsTUI = true
				}
			}
		}
		if isTUIConsumerFile(path) {
			refs, err := tuiComponentRefs(path)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			for _, name := range refs {
				if !wiredTUIComponents[name] {
					failures = append(failures, fmt.Sprintf("%s: consumes packages/tui symbol %s which is not on the wiredTUIComponents allowlist; add it there (and to docs/TS_COMPATIBILITY.md) to deliberately wire another component (P1-F1)", path, name))
				}
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for dir, stat := range stats {
		if !checksPackageShape(dir) {
			continue
		}
		if !stat.HasDoc {
			failures = append(failures, fmt.Sprintf("%s: package must include doc.go with package-level documentation", dir))
		}
		limit := packageLimitFor(dir)
		if stat.Files > limit.MaxFiles || stat.Lines > limit.MaxLines {
			failures = append(failures, fmt.Sprintf("%s: package has %d files/%d lines; limit is %d files/%d lines (%s)",
				dir, stat.Files, stat.Lines, limit.MaxFiles, limit.MaxLines, limit.Reason))
		}
	}
	if hasAgentPackageFiles && !codingAgentImportsAgent {
		failures = append(failures, "packages/agent has implementation files but is not wired into packages/coding-agent")
	}
	if hasTUIPackageFiles && !codingAgentImportsTUI {
		failures = append(failures, "packages/tui has implementation files but is not wired into packages/coding-agent")
	}
	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(1)
	}
}

func checksPackageShape(dir string) bool {
	return strings.HasPrefix(dir, "cmd/") || strings.HasPrefix(dir, "packages/")
}

func packageLimitFor(dir string) packageLimit {
	if limit, ok := temporaryPackageLimits[dir]; ok {
		return limit
	}
	return packageLimit{
		MaxFiles: defaultMaxPackageFiles,
		MaxLines: defaultMaxPackageLines,
		Reason:   "target architecture P7",
	}
}

func fileLineLimitFor(path string) packageLimit {
	if limit, ok := temporaryFileLineLimits[path]; ok {
		return limit
	}
	return packageLimit{
		MaxLines: defaultMaxFileLines,
		Reason:   "single-file maintainability budget",
	}
}

func fileLineStats(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	lines := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	header := string(data)
	if len(header) > 512 {
		header = header[:512]
	}
	generated := strings.Contains(header, "Code generated") && strings.Contains(header, "DO NOT EDIT")
	return lines, generated, nil
}

func fileImports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	return imports, nil
}

func fileTypeAliases(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var aliases []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && typeSpec.Assign.IsValid() {
				aliases = append(aliases, typeSpec.Name.Name)
			}
		}
	}
	return aliases, nil
}

// isTUIConsumerFile reports whether path is a production (non-test) Go file in a
// package that is allowed to consume packages/tui, i.e. cmd/ or
// packages/coding-agent/ but not packages/tui itself.
func isTUIConsumerFile(path string) bool {
	if strings.HasPrefix(path, "packages/tui/") {
		return false
	}
	return strings.HasPrefix(path, "cmd/") || strings.HasPrefix(path, "packages/coding-agent/")
}

// tuiComponentRefs returns the exported packages/tui symbols referenced via a
// selector expression (e.g. tui.NewMarkdown) in path. It resolves the local
// import name for packages/tui so renamed imports are handled, and returns nil
// when the file does not import packages/tui.
func tuiComponentRefs(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	tuiName := ""
	tuiImport := modulePath + "/packages/tui"
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if value != tuiImport {
			continue
		}
		if spec.Name != nil {
			tuiName = spec.Name.Name
		} else {
			tuiName = "tui"
		}
	}
	if tuiName == "" || tuiName == "_" || tuiName == "." {
		return nil, nil
	}
	seen := map[string]bool{}
	var refs []string
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != tuiName {
			return true
		}
		name := sel.Sel.Name
		if name == "" || !ast.IsExported(name) || seen[name] {
			return true
		}
		seen[name] = true
		refs = append(refs, name)
		return true
	})
	return refs, nil
}

func checkImport(filePath, importPath string) []string {
	if !strings.HasPrefix(importPath, modulePath+"/") {
		return nil
	}
	relImport := strings.TrimPrefix(importPath, modulePath+"/")
	var failures []string
	if strings.HasPrefix(filePath, "cmd/pi/") && relImport != "packages/coding-agent" {
		failures = append(failures, fmt.Sprintf("%s imports %s; cmd/pi may only import packages/coding-agent", filePath, importPath))
	}
	if strings.HasPrefix(relImport, "internal/") {
		failures = append(failures, fmt.Sprintf("%s imports %s; internal packages are not allowed", filePath, importPath))
	}
	switch {
	case strings.HasPrefix(filePath, "packages/ai/"):
		if importsAny(relImport, "packages/agent", "packages/tui", "packages/coding-agent") {
			failures = append(failures, fmt.Sprintf("%s imports %s; packages/ai must stay at the bottom of the DAG", filePath, importPath))
		}
	case strings.HasPrefix(filePath, "packages/agent/"):
		if importsAny(relImport, "packages/tui", "packages/coding-agent") {
			failures = append(failures, fmt.Sprintf("%s imports %s; packages/agent may only depend on packages/ai", filePath, importPath))
		}
	case strings.HasPrefix(filePath, "packages/tui/"):
		if strings.HasPrefix(relImport, "packages/") {
			failures = append(failures, fmt.Sprintf("%s imports %s; packages/tui must not depend on other pi packages", filePath, importPath))
		}
	case strings.HasPrefix(filePath, "packages/coding-agent/"):
		if strings.HasPrefix(relImport, "packages/coding-agent") {
			return failures
		}
		if importsAny(relImport, "packages/ai", "packages/agent", "packages/tui") {
			return failures
		}
		if strings.HasPrefix(relImport, "packages/") {
			failures = append(failures, fmt.Sprintf("%s imports %s; packages/coding-agent may only depend on ai, agent, tui, and its subpackages", filePath, importPath))
		}
	}
	return failures
}

func importsAny(importPath string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}
