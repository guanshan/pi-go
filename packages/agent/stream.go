package agent

import (
	"context"

	"github.com/guanshan/pi-go/packages/ai"
)

func DefaultStreamFn(reg *ai.ModelRegistry) StreamFn {
	return func(ctx context.Context, model ai.Model, c ai.Context, o ai.StreamOptions) AssistantStream {
		if reg != nil {
			return reg.Stream(ctx, model, c, o)
		}
		return ai.Stream(ctx, model, c, o)
	}
}

func defaultConvertToLLM(messages []AgentMessage) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(messages))
	for _, msg := range messages {
		switch msg.(type) {
		case ai.UserMessage, *ai.UserMessage, ai.AssistantMessage, *ai.AssistantMessage, ai.ToolResultMessage, *ai.ToolResultMessage:
			out = append(out, msg)
		}
	}
	return out, nil
}

func toolsToAI(tools []AgentTool) []ai.Tool {
	out := make([]ai.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		out = append(out, ai.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Schema(),
		})
	}
	return out
}
