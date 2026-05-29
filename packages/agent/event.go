package agent

import (
	"encoding/json"

	"github.com/guanshan/pi-go/packages/ai"
)

type AgentEvent interface {
	agentEventTag()
}

type AgentStartEvent struct{}

type AgentEndEvent struct {
	Messages []AgentMessage
}

type TurnStartEvent struct{}

type TurnEndEvent struct {
	Message     AgentMessage
	ToolResults []ai.ToolResultMessage
}

type MessageStartEvent struct {
	Message AgentMessage
}

type MessageUpdateEvent struct {
	Message        AgentMessage
	AssistantEvent ai.AssistantMessageEvent
}

type MessageEndEvent struct {
	Message AgentMessage
}

type ToolExecutionStartEvent struct {
	ToolCallID string
	ToolName   string
	Args       json.RawMessage
}

type ToolExecutionUpdateEvent struct {
	ToolCallID    string
	ToolName      string
	Args          json.RawMessage
	PartialResult AgentToolResult
}

type ToolExecutionEndEvent struct {
	ToolCallID string
	ToolName   string
	Result     AgentToolResult
	IsError    bool
}

func (AgentStartEvent) agentEventTag()          {}
func (AgentEndEvent) agentEventTag()            {}
func (TurnStartEvent) agentEventTag()           {}
func (TurnEndEvent) agentEventTag()             {}
func (MessageStartEvent) agentEventTag()        {}
func (MessageUpdateEvent) agentEventTag()       {}
func (MessageEndEvent) agentEventTag()          {}
func (ToolExecutionStartEvent) agentEventTag()  {}
func (ToolExecutionUpdateEvent) agentEventTag() {}
func (ToolExecutionEndEvent) agentEventTag()    {}

func AgentEventType(ev AgentEvent) string {
	switch ev.(type) {
	case AgentStartEvent:
		return "agent_start"
	case AgentEndEvent:
		return "agent_end"
	case TurnStartEvent:
		return "turn_start"
	case TurnEndEvent:
		return "turn_end"
	case MessageStartEvent:
		return "message_start"
	case MessageUpdateEvent:
		return "message_update"
	case MessageEndEvent:
		return "message_end"
	case ToolExecutionStartEvent:
		return "tool_execution_start"
	case ToolExecutionUpdateEvent:
		return "tool_execution_update"
	case ToolExecutionEndEvent:
		return "tool_execution_end"
	default:
		return ""
	}
}
