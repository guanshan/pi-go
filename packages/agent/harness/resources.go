package harness

import (
	"context"
	"fmt"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func (h *AgentHarness) Skill(ctx context.Context, name string, additionalInstructions string) (ai.AssistantMessage, error) {
	resources := h.GetResources()
	for _, skill := range resources.Skills {
		if skill.Name != name {
			continue
		}
		return h.Prompt(ctx, formatSkillInvocation(skill, additionalInstructions), PromptOptions{})
	}
	return ai.AssistantMessage{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: fmt.Sprintf("skill %q not found", name)}
}

func (h *AgentHarness) PromptFromTemplate(ctx context.Context, name string, args []string) (ai.AssistantMessage, error) {
	resources := h.GetResources()
	for _, tmpl := range resources.PromptTemplates {
		if tmpl.Name == name {
			return h.Prompt(ctx, FormatPromptTemplateInvocation(tmpl, args), PromptOptions{})
		}
	}
	return ai.AssistantMessage{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: fmt.Sprintf("prompt template %q not found", name)}
}
