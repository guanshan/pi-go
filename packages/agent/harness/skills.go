package harness

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/guanshan/pi-go/packages/agent/gitignore"
	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
)

const (
	maxSkillNameLength        = 64
	maxSkillDescriptionLength = 1024
)

var skillIgnoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

func LoadSkills(ctx context.Context, env harnessenv.ExecutionEnv, dirs ...string) SkillLoadResult {
	if ctx == nil {
		ctx = context.Background()
	}
	var result SkillLoadResult
	if env == nil {
		result.Diagnostics = append(result.Diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    "file_info_failed",
			Message: "execution environment is nil",
		})
		return result
	}
	for _, dir := range dirs {
		info, err := env.FileInfo(ctx, dir)
		if err != nil {
			if !isFileNotFound(err) {
				result.Diagnostics = append(result.Diagnostics, skillDiagnostic("file_info_failed", err, dir))
			}
			continue
		}
		kind, ok := resolveSkillKind(ctx, env, info, &result.Diagnostics)
		if !ok || kind != harnessenv.FileKindDirectory {
			continue
		}
		loaded := loadSkillsFromDir(ctx, env, info.Path, true, gitignore.New(), info.Path)
		result.Skills = append(result.Skills, loaded.Skills...)
		result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
	}
	return result
}

func LoadSourcedSkills(ctx context.Context, env harnessenv.ExecutionEnv, inputs []SourcedSkillInput) SourcedSkillLoadResult {
	var result SourcedSkillLoadResult
	for _, input := range inputs {
		loaded := LoadSkills(ctx, env, input.Path)
		for _, skill := range loaded.Skills {
			result.Skills = append(result.Skills, SourcedSkill{Skill: skill, Source: input.Source})
		}
		for _, diagnostic := range loaded.Diagnostics {
			diagnostic.Source = input.Source
			result.Diagnostics = append(result.Diagnostics, diagnostic)
		}
	}
	return result
}

func FormatSkillInvocation(skill Skill, additionalInstructions string) string {
	return formatSkillInvocation(skill, additionalInstructions)
}

func loadSkillsFromDir(ctx context.Context, env harnessenv.ExecutionEnv, dir string, includeRootFiles bool, ignores gitignore.Matcher, rootDir string) SkillLoadResult {
	var result SkillLoadResult
	dirInfo, err := env.FileInfo(ctx, dir)
	if err != nil {
		if !isFileNotFound(err) {
			result.Diagnostics = append(result.Diagnostics, skillDiagnostic("file_info_failed", err, dir))
		}
		return result
	}
	kind, ok := resolveSkillKind(ctx, env, dirInfo, &result.Diagnostics)
	if !ok || kind != harnessenv.FileKindDirectory {
		return result
	}
	addSkillIgnoreRules(ctx, env, &ignores, dir, rootDir, &result.Diagnostics)
	entries, err := env.ListDir(ctx, dir)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, skillDiagnostic("list_failed", err, dir))
		return result
	}

	for _, entry := range entries {
		if entry.Name != "SKILL.md" {
			continue
		}
		entryKind, ok := resolveSkillKind(ctx, env, entry, &result.Diagnostics)
		if !ok || entryKind != harnessenv.FileKindFile {
			continue
		}
		if ignores.Ignores(relativeEnvPath(rootDir, entry.Path)) {
			continue
		}
		skill, diagnostics := loadSkillFromFile(ctx, env, entry.Path)
		if skill != nil {
			result.Skills = append(result.Skills, *skill)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		return result
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" {
			continue
		}
		entryKind, ok := resolveSkillKind(ctx, env, entry, &result.Diagnostics)
		if !ok {
			continue
		}
		relPath := relativeEnvPath(rootDir, entry.Path)
		ignorePath := relPath
		if entryKind == harnessenv.FileKindDirectory {
			ignorePath += "/"
		}
		if ignores.Ignores(ignorePath) {
			continue
		}
		if entryKind == harnessenv.FileKindDirectory {
			loaded := loadSkillsFromDir(ctx, env, entry.Path, false, ignores, rootDir)
			result.Skills = append(result.Skills, loaded.Skills...)
			result.Diagnostics = append(result.Diagnostics, loaded.Diagnostics...)
			continue
		}
		if entryKind != harnessenv.FileKindFile || !includeRootFiles || !strings.EqualFold(path.Ext(entry.Name), ".md") {
			continue
		}
		skill, diagnostics := loadSkillFromFile(ctx, env, entry.Path)
		if skill != nil {
			result.Skills = append(result.Skills, *skill)
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	return result
}

func addSkillIgnoreRules(ctx context.Context, env harnessenv.ExecutionEnv, ignores *gitignore.Matcher, dir string, rootDir string, diagnostics *[]SkillDiagnostic) {
	relativeDir := relativeEnvPath(rootDir, dir)
	prefix := ""
	if relativeDir != "" {
		prefix = relativeDir + "/"
	}
	for _, filename := range skillIgnoreFileNames {
		ignorePath := joinEnvPath(dir, filename)
		info, err := env.FileInfo(ctx, ignorePath)
		if err != nil {
			if !isFileNotFound(err) {
				*diagnostics = append(*diagnostics, skillDiagnostic("file_info_failed", err, ignorePath))
			}
			continue
		}
		kind, ok := resolveSkillKind(ctx, env, info, diagnostics)
		if !ok || kind != harnessenv.FileKindFile {
			continue
		}
		content, err := env.ReadTextFile(ctx, ignorePath)
		if err != nil {
			*diagnostics = append(*diagnostics, skillDiagnostic("read_failed", err, ignorePath))
			continue
		}
		for _, line := range strings.Split(normalizeNewlines(content), "\n") {
			if pattern := gitignore.PrefixPattern(line, prefix); pattern != "" {
				ignores.Add(pattern)
			}
		}
	}
}

func loadSkillFromFile(ctx context.Context, env harnessenv.ExecutionEnv, filePath string) (*Skill, []SkillDiagnostic) {
	raw, err := env.ReadTextFile(ctx, filePath)
	if err != nil {
		return nil, []SkillDiagnostic{skillDiagnostic("read_failed", err, filePath)}
	}
	frontmatter, body, err := parseFrontmatterYAML(raw)
	if err != nil {
		return nil, []SkillDiagnostic{skillDiagnostic("parse_failed", err, filePath)}
	}
	skillDir := dirEnvPath(filePath)
	parentDirName := baseEnvPath(skillDir)
	description, _ := frontmatter["description"].(string)

	var diagnostics []SkillDiagnostic
	for _, message := range validateSkillDescription(description) {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    "invalid_metadata",
			Message: message,
			Path:    filePath,
		})
	}
	name := parentDirName
	if frontmatterName, ok := frontmatter["name"].(string); ok {
		name = frontmatterName
	}
	for _, message := range validateSkillName(name, parentDirName) {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    "invalid_metadata",
			Message: message,
			Path:    filePath,
		})
	}
	if strings.TrimSpace(description) == "" {
		return nil, diagnostics
	}
	return &Skill{
		Name:                   name,
		Description:            description,
		Content:                body,
		FilePath:               filePath,
		DisableModelInvocation: frontmatter["disable-model-invocation"] == true,
	}, diagnostics
}

func validateSkillName(name string, parentDirName string) []string {
	var errors []string
	if name != parentDirName {
		errors = append(errors, fmt.Sprintf("name %q does not match parent directory %q", name, parentDirName))
	}
	if len(name) > maxSkillNameLength {
		errors = append(errors, fmt.Sprintf("name exceeds %d characters (%d)", maxSkillNameLength, len(name)))
	}
	if !isValidSkillNameCharacters(name) {
		errors = append(errors, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errors = append(errors, "name must not start or end with a hyphen")
	}
	if strings.Contains(name, "--") {
		errors = append(errors, "name must not contain consecutive hyphens")
	}
	return errors
}

func isValidSkillNameCharacters(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		return false
	}
	return true
}

func validateSkillDescription(description string) []string {
	if strings.TrimSpace(description) == "" {
		return []string{"description is required"}
	}
	if len(description) > maxSkillDescriptionLength {
		return []string{fmt.Sprintf("description exceeds %d characters (%d)", maxSkillDescriptionLength, len(description))}
	}
	return nil
}

func resolveSkillKind(ctx context.Context, env harnessenv.ExecutionEnv, info harnessenv.FileInfo, diagnostics *[]SkillDiagnostic) (harnessenv.FileKind, bool) {
	if info.Kind == harnessenv.FileKindFile || info.Kind == harnessenv.FileKindDirectory {
		return info.Kind, true
	}
	if info.Kind != harnessenv.FileKindSymlink {
		return "", false
	}
	canonical, err := env.CanonicalPath(ctx, info.Path)
	if err != nil {
		if !isFileNotFound(err) {
			*diagnostics = append(*diagnostics, skillDiagnostic("file_info_failed", err, info.Path))
		}
		return "", false
	}
	target, err := env.FileInfo(ctx, canonical)
	if err != nil {
		if !isFileNotFound(err) {
			*diagnostics = append(*diagnostics, skillDiagnostic("file_info_failed", err, info.Path))
		}
		return "", false
	}
	if target.Kind == harnessenv.FileKindFile || target.Kind == harnessenv.FileKindDirectory {
		return target.Kind, true
	}
	return "", false
}

func skillDiagnostic(code string, err error, p string) SkillDiagnostic {
	return SkillDiagnostic{Type: "warning", Code: code, Message: errorMessage(err), Path: p}
}

func formatSkillInvocation(skill Skill, additionalInstructions string) string {
	block := fmt.Sprintf("<skill name=\"%s\" location=\"%s\">\nReferences are relative to %s.\n\n%s\n</skill>",
		skill.Name,
		skill.FilePath,
		dirEnvPath(skill.FilePath),
		skill.Content,
	)
	if additionalInstructions == "" {
		return block
	}
	return block + "\n\n" + additionalInstructions
}
