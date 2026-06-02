package harness

import (
	"context"
	"fmt"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func (h *AgentHarness) Skill(ctx context.Context, name string, additionalInstructions string) (final ai.AssistantMessage, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := h.beginRun(ctx, PhaseTurn)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	gen := h.currentRunGeneration()
	defer func() {
		if flushErr := h.flushPendingSessionWrites(ctx); flushErr != nil && err == nil {
			err = flushErr
		}
		release()
	}()
	state, err := h.createTurnState(ctx)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	// Resolve the skill from the same turn snapshot used for the run so a
	// concurrent resource change cannot make the lookup and the prompt diverge.
	for _, skill := range state.resources.Skills {
		if skill.Name != name {
			continue
		}
		return h.runTurn(ctx, gen, state, formatSkillInvocation(skill, additionalInstructions), PromptOptions{})
	}
	return ai.AssistantMessage{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: fmt.Sprintf("skill %q not found", name)}
}

func (h *AgentHarness) PromptFromTemplate(ctx context.Context, name string, args []string) (final ai.AssistantMessage, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := h.beginRun(ctx, PhaseTurn)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	gen := h.currentRunGeneration()
	defer func() {
		if flushErr := h.flushPendingSessionWrites(ctx); flushErr != nil && err == nil {
			err = flushErr
		}
		release()
	}()
	state, err := h.createTurnState(ctx)
	if err != nil {
		return ai.AssistantMessage{}, err
	}
	// Resolve the template from the same turn snapshot used for the run.
	for _, tmpl := range state.resources.PromptTemplates {
		if tmpl.Name == name {
			return h.runTurn(ctx, gen, state, FormatPromptTemplateInvocation(tmpl, args), PromptOptions{})
		}
	}
	return ai.AssistantMessage{}, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: fmt.Sprintf("prompt template %q not found", name)}
}
