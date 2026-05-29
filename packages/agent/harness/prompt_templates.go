package harness

import (
	"context"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
	"gopkg.in/yaml.v3"
)

func LoadPromptTemplates(ctx context.Context, env harnessenv.ExecutionEnv, paths ...string) PromptTemplateLoadResult {
	if ctx == nil {
		ctx = context.Background()
	}
	var result PromptTemplateLoadResult
	if env == nil {
		result.Diagnostics = append(result.Diagnostics, PromptTemplateDiagnostic{
			Type:    "warning",
			Code:    "file_info_failed",
			Message: "execution environment is nil",
		})
		return result
	}
	for _, inputPath := range paths {
		info, err := env.FileInfo(ctx, inputPath)
		if err != nil {
			if !isFileNotFound(err) {
				result.Diagnostics = append(result.Diagnostics, promptTemplateDiagnostic("file_info_failed", err, inputPath))
			}
			continue
		}
		kind, ok := resolvePromptTemplateKind(ctx, env, info, &result.Diagnostics)
		if !ok {
			continue
		}
		switch {
		case kind == harnessenv.FileKindDirectory:
			loaded := loadPromptTemplatesFromDir(ctx, env, info.Path)
			result.PromptTemplates = append(result.PromptTemplates, loaded.PromptTemplates...)
			result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
		case kind == harnessenv.FileKindFile && strings.EqualFold(path.Ext(info.Name), ".md"):
			tmpl, diagnostics := loadPromptTemplateFromFile(ctx, env, info.Path)
			if tmpl != nil {
				result.PromptTemplates = append(result.PromptTemplates, *tmpl)
			}
			result.Diagnostics = append(result.Diagnostics, diagnostics...)
		}
	}
	return result
}

func LoadSourcedPromptTemplates(ctx context.Context, env harnessenv.ExecutionEnv, inputs []SourcedPromptTemplateInput) SourcedPromptTemplateLoadResult {
	var result SourcedPromptTemplateLoadResult
	for _, input := range inputs {
		loaded := LoadPromptTemplates(ctx, env, input.Path)
		for _, tmpl := range loaded.PromptTemplates {
			result.PromptTemplates = append(result.PromptTemplates, SourcedPromptTemplate{
				PromptTemplate: tmpl,
				Source:         input.Source,
			})
		}
		for _, diagnostic := range loaded.Diagnostics {
			diagnostic.Source = input.Source
			result.Diagnostics = append(result.Diagnostics, diagnostic)
		}
	}
	return result
}

func loadPromptTemplatesFromDir(ctx context.Context, env harnessenv.ExecutionEnv, dir string) PromptTemplateLoadResult {
	entries, err := env.ListDir(ctx, dir)
	if err != nil {
		return PromptTemplateLoadResult{Diagnostics: []PromptTemplateDiagnostic{promptTemplateDiagnostic("list_failed", err, dir)}}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	var result PromptTemplateLoadResult
	for _, entry := range entries {
		kind, ok := resolvePromptTemplateKind(ctx, env, entry, &result.Diagnostics)
		if !ok || kind != harnessenv.FileKindFile || !strings.EqualFold(path.Ext(entry.Name), ".md") {
			continue
		}
		tmpl, diagnostics := loadPromptTemplateFromFile(ctx, env, entry.Path)
		if tmpl != nil {
			result.PromptTemplates = append(result.PromptTemplates, *tmpl)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	return result
}

func loadPromptTemplateFromFile(ctx context.Context, env harnessenv.ExecutionEnv, filePath string) (*PromptTemplate, []PromptTemplateDiagnostic) {
	raw, err := env.ReadTextFile(ctx, filePath)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{promptTemplateDiagnostic("read_failed", err, filePath)}
	}
	frontmatter, body, err := parseFrontmatterYAML(raw)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{promptTemplateDiagnostic("parse_failed", err, filePath)}
	}
	description, _ := frontmatter["description"].(string)
	if strings.TrimSpace(description) == "" {
		if firstLine := firstNonEmptyLine(body); firstLine != "" {
			description = firstLine
			if len(description) > 60 {
				description = description[:60] + "..."
			}
		}
	}
	return &PromptTemplate{
		Name:        strings.TrimSuffix(baseEnvPath(filePath), path.Ext(baseEnvPath(filePath))),
		Description: description,
		Content:     body,
	}, nil
}

func resolvePromptTemplateKind(ctx context.Context, env harnessenv.ExecutionEnv, info harnessenv.FileInfo, diagnostics *[]PromptTemplateDiagnostic) (harnessenv.FileKind, bool) {
	if info.Kind == harnessenv.FileKindFile || info.Kind == harnessenv.FileKindDirectory {
		return info.Kind, true
	}
	if info.Kind != harnessenv.FileKindSymlink {
		return "", false
	}
	canonical, err := env.CanonicalPath(ctx, info.Path)
	if err != nil {
		if !isFileNotFound(err) {
			*diagnostics = append(*diagnostics, promptTemplateDiagnostic("file_info_failed", err, info.Path))
		}
		return "", false
	}
	target, err := env.FileInfo(ctx, canonical)
	if err != nil {
		if !isFileNotFound(err) {
			*diagnostics = append(*diagnostics, promptTemplateDiagnostic("file_info_failed", err, info.Path))
		}
		return "", false
	}
	if target.Kind == harnessenv.FileKindFile || target.Kind == harnessenv.FileKindDirectory {
		return target.Kind, true
	}
	return "", false
}

func promptTemplateDiagnostic(code string, err error, p string) PromptTemplateDiagnostic {
	return PromptTemplateDiagnostic{Type: "warning", Code: code, Message: errorMessage(err), Path: p}
}

func ParseCommandArgs(input string) []string {
	var args []string
	var b strings.Builder
	var quote rune
	inArg := false
	for _, r := range strings.TrimSpace(input) {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			inArg = true
		case r == '\'' || r == '"':
			quote = r
			inArg = true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, b.String())
				b.Reset()
				inArg = false
			}
		default:
			b.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		args = append(args, b.String())
	}
	return args
}

func FormatPromptTemplateInvocation(tmpl PromptTemplate, args []string) string {
	return SubstitutePromptArgs(tmpl.Content, args)
}

func SubstitutePromptArgs(content string, args []string) string {
	result := content
	result = replaceDollarNumber(result, args)
	result = replaceArgSlices(result, args)
	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)
	return result
}

func replaceDollarNumber(input string, args []string) string {
	var out strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' || i+1 >= len(input) || input[i+1] < '0' || input[i+1] > '9' {
			out.WriteByte(input[i])
			i++
			continue
		}
		j := i + 1
		for j < len(input) && input[j] >= '0' && input[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(input[i+1 : j])
		if n >= 1 && n <= len(args) {
			out.WriteString(args[n-1])
		}
		i = j
	}
	return out.String()
}

func replaceArgSlices(input string, args []string) string {
	var out strings.Builder
	for i := 0; i < len(input); {
		if !strings.HasPrefix(input[i:], "${@:") {
			out.WriteByte(input[i])
			i++
			continue
		}
		end := strings.IndexByte(input[i:], '}')
		if end < 0 {
			out.WriteByte(input[i])
			i++
			continue
		}
		expr := input[i+4 : i+end]
		parts := strings.Split(expr, ":")
		if len(parts) < 1 || len(parts) > 2 {
			out.WriteString(input[i : i+end+1])
			i += end + 1
			continue
		}
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			out.WriteString(input[i : i+end+1])
			i += end + 1
			continue
		}
		start--
		if start < 0 {
			start = 0
		}
		stop := len(args)
		if len(parts) == 2 {
			length, err := strconv.Atoi(parts[1])
			if err != nil {
				out.WriteString(input[i : i+end+1])
				i += end + 1
				continue
			}
			stop = start + length
			if stop > len(args) {
				stop = len(args)
			}
		}
		if start < len(args) && start < stop {
			out.WriteString(strings.Join(args[start:stop], " "))
		}
		i += end + 1
	}
	return out.String()
}

func ParseFrontmatter(content string) (map[string]any, string) {
	frontmatter, body, err := parseFrontmatterYAML(content)
	if err != nil {
		return map[string]any{}, normalizeNewlines(content)
	}
	return frontmatter, body
}

func StripFrontmatter(content string) string {
	_, body := ParseFrontmatter(content)
	return body
}

func parseFrontmatterYAML(content string) (map[string]any, string, error) {
	normalized := normalizeNewlines(content)
	if !strings.HasPrefix(normalized, "---") {
		return map[string]any{}, normalized, nil
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex < 0 {
		return map[string]any{}, normalized, nil
	}
	endIndex += 3
	yamlText := normalized[4:endIndex]
	body := strings.TrimSpace(normalized[endIndex+4:])
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlText), &parsed); err != nil {
		return nil, "", err
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	return parsed, body, nil
}

func normalizeNewlines(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

func isFileNotFound(err error) bool {
	var fileErr *harnessenv.FileError
	return errors.As(err, &fileErr) && fileErr.Code == harnessenv.FileErrNotFound
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinEnvPath(base string, child string) string {
	if base == "" || base == "/" {
		return "/" + strings.TrimLeft(child, "/")
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(child, "/")
}

func dirEnvPath(p string) string {
	normalized := strings.TrimRight(p, "/")
	idx := strings.LastIndex(normalized, "/")
	if idx <= 0 {
		return "/"
	}
	return normalized[:idx]
}

func baseEnvPath(p string) string {
	normalized := strings.TrimRight(p, "/")
	idx := strings.LastIndex(normalized, "/")
	if idx < 0 {
		return normalized
	}
	return normalized[idx+1:]
}

func relativeEnvPath(root string, p string) string {
	root = strings.TrimRight(root, "/")
	p = strings.TrimRight(p, "/")
	if p == root {
		return ""
	}
	if strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:]
	}
	return strings.TrimLeft(p, "/")
}
