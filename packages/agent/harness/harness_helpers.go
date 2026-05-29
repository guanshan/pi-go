package harness

import (
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

func buildToolMap(tools []agent.AgentTool) (map[string]agent.AgentTool, []string, error) {
	byName := map[string]agent.AgentTool{}
	order := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if _, exists := byName[name]; exists {
			return nil, nil, &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "duplicate tool name(s): " + name}
		}
		byName[name] = tool
		order = append(order, name)
	}
	return byName, order, nil
}

func validateActiveToolNames(names []string, tools map[string]agent.AgentTool) error {
	seen := map[string]bool{}
	var duplicates []string
	var missing []string
	for _, name := range names {
		if seen[name] {
			duplicates = append(duplicates, name)
		}
		seen[name] = true
		if tools[name] == nil {
			missing = append(missing, name)
		}
	}
	if len(duplicates) > 0 {
		return &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "duplicate active tool name(s): " + strings.Join(duplicates, ", ")}
	}
	if len(missing) > 0 {
		return &agent.AgentError{Code: agent.AgentErrInvalidArgument, Msg: "unknown tool(s): " + strings.Join(missing, ", ")}
	}
	return nil
}

func streamError(model ai.Model, err error) agent.AssistantStream {
	stream := ai.NewAssistantMessageEventStream(1)
	msg := ai.NewAssistantMessageForModel(model, nil, ai.Usage{}, "error")
	msg.ErrorMessage = err.Error()
	stream.Push(ai.AssistantMessageEvent{Type: "error", Reason: "error", Error: msg})
	return stream
}

func mergeStringMaps(values ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		for k, v := range value {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeAnyMaps(values ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, value := range values {
		for k, v := range value {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
