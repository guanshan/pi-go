package harness

import (
	"context"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

// DefaultSystemPrompt is used when no system prompt is configured. It mirrors
// the TS harness, which defaults to this string in createTurnState.
const DefaultSystemPrompt = "You are a helpful assistant."

type SystemPromptSource struct {
	Static  string
	Builder SystemPromptBuilder
	// staticSet distinguishes an explicit static prompt (including the empty
	// string) from a zero-value, unconfigured source. An unconfigured source
	// resolves to DefaultSystemPrompt.
	staticSet bool
}

type SystemPromptBuilder func(ctx context.Context, sc SystemPromptContext) (string, error)

type SystemPromptContext struct {
	Env           harnessenv.ExecutionEnv
	Session       *session.Session
	Model         ai.Model
	ThinkingLevel ai.ThinkingLevel
	ActiveTools   []agent.AgentTool
	Resources     Resources
}

func StaticSystemPrompt(value string) SystemPromptSource {
	return SystemPromptSource{Static: value, staticSet: true}
}

func DynamicSystemPrompt(builder SystemPromptBuilder) SystemPromptSource {
	return SystemPromptSource{Builder: builder}
}

func (s SystemPromptSource) Build(ctx context.Context, sc SystemPromptContext) (string, error) {
	if s.Builder != nil {
		return s.Builder(ctx, sc)
	}
	if !s.staticSet {
		return DefaultSystemPrompt, nil
	}
	return s.Static, nil
}

func FormatSkillsForSystemPrompt(skills []Skill) string {
	visible := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Read the full skill file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, skill := range visible {
		lines = append(lines,
			"  <skill>",
			"    <name>"+escapeXML(skill.Name)+"</name>",
			"    <description>"+escapeXML(skill.Description)+"</description>",
			"    <location>"+escapeXML(skill.FilePath)+"</location>",
			"  </skill>",
		)
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
