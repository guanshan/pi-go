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
	"packages/agent/harness":           {MaxFiles: 16, MaxLines: 3600, Reason: "ratcheted budget; awaits full prompt/session/hook subpackage extraction"},
	"packages/ai":                      {MaxFiles: 33, MaxLines: 8000, Reason: "remaining oauth/model catalog/root provider adapters await final package split"},
	"packages/ai/providers":            {MaxFiles: 15, MaxLines: 5200, Reason: "provider protocol implementations migrated from packages/ai root; split by provider family if this grows further"},
	"packages/coding-agent":            {MaxFiles: 15, MaxLines: 4820, Reason: "ratcheted package-manager rollback/dependency budget; awaits resource-loader and package-manager/config split"},
	"packages/coding-agent/core":       {MaxFiles: 28, MaxLines: 10780, Reason: "ratcheted P0/P1 session/runtime parity budget; awaits runtime/session/modes subpackage split"},
	"packages/coding-agent/core/tools": {MaxFiles: 20, MaxLines: 2200, Reason: "ratcheted budget; intentionally one file per tool but split if tool bodies grow"},
	"packages/tui":                     {MaxFiles: 30, MaxLines: 6400, Reason: "ratcheted TUI primitive budget; revisit with subpackages if this grows"},
}

var temporaryFileLineLimits = map[string]packageLimit{}

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
