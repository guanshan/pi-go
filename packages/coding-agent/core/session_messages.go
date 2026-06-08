package core

import (
	"github.com/guanshan/pi-go/packages/ai"
)

type CustomSessionMessage struct {
	Role        string `json:"role,omitempty"`
	CustomType  string `json:"customType,omitempty"`
	Content     any    `json:"content,omitempty"`
	Display     bool   `json:"display,omitempty"`
	Details     any    `json:"details,omitempty"`
	TimestampMs int64  `json:"timestamp,omitempty"`
}

func (m CustomSessionMessage) MessageRole() string { return firstNonEmpty(m.Role, "custom") }
func (m CustomSessionMessage) Timestamp() int64    { return m.TimestampMs }
func (m CustomSessionMessage) ContentBlocks() []ai.ContentBlock {
	blocks, _ := ai.CustomContentBlocks(m.Content)
	return blocks
}
func (m CustomSessionMessage) ProviderContentBlocks() []ai.ContentBlock {
	return m.ContentBlocks()
}

type BranchSummaryMessage struct {
	Role        string `json:"role,omitempty"`
	Summary     string `json:"summary,omitempty"`
	FromID      string `json:"fromId,omitempty"`
	TimestampMs int64  `json:"timestamp,omitempty"`
}

func (m BranchSummaryMessage) MessageRole() string { return firstNonEmpty(m.Role, "branchSummary") }
func (m BranchSummaryMessage) Timestamp() int64    { return m.TimestampMs }
func (m BranchSummaryMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(m.Summary)
}
func (m BranchSummaryMessage) ProviderContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(ai.BranchSummaryText(m.Summary))
}

type CompactionSummaryMessage struct {
	Role         string `json:"role,omitempty"`
	Summary      string `json:"summary,omitempty"`
	TokensBefore int    `json:"tokensBefore,omitempty"`
	TimestampMs  int64  `json:"timestamp,omitempty"`
}

func (m CompactionSummaryMessage) MessageRole() string {
	return firstNonEmpty(m.Role, "compactionSummary")
}
func (m CompactionSummaryMessage) Timestamp() int64 { return m.TimestampMs }
func (m CompactionSummaryMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(m.Summary)
}
func (m CompactionSummaryMessage) ProviderContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(ai.CompactionSummaryText(m.Summary))
}

type BashExecutionMessage struct {
	Role               string `json:"role,omitempty"`
	Command            string `json:"command,omitempty"`
	Output             string `json:"output,omitempty"`
	ExitCode           *int   `json:"exitCode,omitempty"`
	Cancelled          bool   `json:"cancelled,omitempty"`
	Truncated          bool   `json:"truncated,omitempty"`
	FullOutputPath     string `json:"fullOutputPath,omitempty"`
	ExcludeFromContext bool   `json:"excludeFromContext,omitempty"`
	TimestampMs        int64  `json:"timestamp,omitempty"`
}

func (m BashExecutionMessage) MessageRole() string { return firstNonEmpty(m.Role, "bashExecution") }
func (m BashExecutionMessage) Timestamp() int64    { return m.TimestampMs }
func (m BashExecutionMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(ai.FormatBashExecutionText(m.Command, m.Output, m.ExitCode, m.Cancelled, m.Truncated, m.FullOutputPath))
}
func (m BashExecutionMessage) ProviderContentBlocks() []ai.ContentBlock {
	if m.ExcludeFromContext {
		return nil
	}
	return m.ContentBlocks()
}

// convertSessionMessagesToLLM is the ConvertToLLM hook for the coding-agent loop.
// It mirrors the agent harness converter: standard user/assistant/toolResult
// messages pass through unchanged, the typed summary/custom/bash messages are
// turned into provider-ready user messages (with the proper <summary> framing
// via ProviderContentBlocks), and legacy ai.CustomMessage entries loaded from
// disk are passed through so the provider's transformMessages step normalizes
// them. The default converter would instead drop every non-standard message,
// stripping compaction/branch summaries and !bash output from the model's view.
func convertSessionMessagesToLLM(messages []ai.Message) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch m := message.(type) {
		case ai.UserMessage, *ai.UserMessage, ai.AssistantMessage, *ai.AssistantMessage, ai.ToolResultMessage, *ai.ToolResultMessage:
			out = append(out, message)
			continue
		case ai.CustomMessage:
			out = append(out, m)
			continue
		case *ai.CustomMessage:
			if m != nil {
				out = append(out, *m)
			}
			continue
		}
		if converted, ok := ai.NormalizeProviderContentMessage(message); ok {
			out = append(out, converted...)
		}
	}
	return out, nil
}

func asBashExecutionMessage(message ai.Message) (BashExecutionMessage, bool) {
	switch m := message.(type) {
	case BashExecutionMessage:
		return m, true
	case *BashExecutionMessage:
		if m == nil {
			return BashExecutionMessage{}, false
		}
		return *m, true
	default:
		custom, ok := ai.AsCustomMessage(message)
		if !ok || custom.MessageRole() != "bashExecution" {
			return BashExecutionMessage{}, false
		}
		return BashExecutionMessage{
			Role:               "bashExecution",
			Command:            custom.Command,
			Output:             custom.Output,
			ExitCode:           custom.ExitCode,
			Cancelled:          custom.Cancelled,
			Truncated:          custom.Truncated,
			FullOutputPath:     custom.FullOutputPath,
			ExcludeFromContext: custom.ExcludeFromContext,
			TimestampMs:        custom.Timestamp(),
		}, true
	}
}

