package agent

import "github.com/guanshan/pi-go/packages/ai"

type AgentState struct {
	SystemPrompt     string
	Model            ai.Model
	ThinkingLevel    ThinkingLevel
	Tools            []AgentTool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamingMessage AgentMessage
	PendingToolCalls map[string]struct{}
	ErrorMessage     string
}

func (a *Agent) reduceStateLocked(ev AgentEvent) {
	switch event := ev.(type) {
	case MessageStartEvent:
		a.state.StreamingMessage = event.Message
	case MessageUpdateEvent:
		a.state.StreamingMessage = event.Message
	case MessageEndEvent:
		a.state.StreamingMessage = nil
		a.state.Messages = append(a.state.Messages, event.Message)
	case ToolExecutionStartEvent:
		a.state.PendingToolCalls = addPendingToolCall(a.state.PendingToolCalls, event.ToolCallID)
	case ToolExecutionEndEvent:
		removePendingToolCall(a.state.PendingToolCalls, event.ToolCallID)
	case TurnEndEvent:
		if assistant, ok := ai.AsAssistantMessage(event.Message); ok && assistant.ErrorMessage != "" {
			a.state.ErrorMessage = assistant.ErrorMessage
		}
	case AgentEndEvent:
		a.state.StreamingMessage = nil
	}
}

func cloneState(state AgentState) AgentState {
	state.Tools = append([]AgentTool(nil), state.Tools...)
	state.Messages = append([]AgentMessage(nil), state.Messages...)
	state.PendingToolCalls = clonePendingToolCalls(state.PendingToolCalls)
	return state
}

func addPendingToolCall(values map[string]struct{}, value string) map[string]struct{} {
	if value == "" {
		return values
	}
	if values == nil {
		values = map[string]struct{}{}
	}
	values[value] = struct{}{}
	return values
}

func removePendingToolCall(values map[string]struct{}, value string) {
	delete(values, value)
}

func clonePendingToolCalls(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for value := range values {
		out[value] = struct{}{}
	}
	return out
}
