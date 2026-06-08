package harness

import (
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

const CompactionSummaryPrefix = ai.CompactionSummaryPrefix
const CompactionSummarySuffix = ai.CompactionSummarySuffix
const BranchSummaryPrefix = ai.BranchSummaryPrefix
const BranchSummarySuffix = ai.BranchSummarySuffix

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

func (m BashExecutionMessage) MessageRole() string { return "bashExecution" }
func (m BashExecutionMessage) Timestamp() int64    { return m.TimestampMs }
func (m BashExecutionMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(BashExecutionToText(m))
}

type CustomMessage struct {
	Role        string `json:"role,omitempty"`
	CustomType  string `json:"customType,omitempty"`
	Content     any    `json:"content,omitempty"`
	Display     bool   `json:"display,omitempty"`
	Details     any    `json:"details,omitempty"`
	TimestampMs int64  `json:"timestamp,omitempty"`
}

func (m CustomMessage) MessageRole() string { return "custom" }
func (m CustomMessage) Timestamp() int64    { return m.TimestampMs }
func (m CustomMessage) ContentBlocks() []ai.ContentBlock {
	blocks, _ := ai.CustomContentBlocks(m.Content)
	return blocks
}

type BranchSummaryMessage struct {
	Role        string `json:"role,omitempty"`
	Summary     string `json:"summary,omitempty"`
	FromID      string `json:"fromId,omitempty"`
	TimestampMs int64  `json:"timestamp,omitempty"`
}

func (m BranchSummaryMessage) MessageRole() string { return "branchSummary" }
func (m BranchSummaryMessage) Timestamp() int64    { return m.TimestampMs }
func (m BranchSummaryMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(ai.BranchSummaryText(m.Summary))
}

type CompactionSummaryMessage struct {
	Role         string `json:"role,omitempty"`
	Summary      string `json:"summary,omitempty"`
	TokensBefore int    `json:"tokensBefore,omitempty"`
	TimestampMs  int64  `json:"timestamp,omitempty"`
}

func (m CompactionSummaryMessage) MessageRole() string { return "compactionSummary" }
func (m CompactionSummaryMessage) Timestamp() int64    { return m.TimestampMs }
func (m CompactionSummaryMessage) ContentBlocks() []ai.ContentBlock {
	return ai.TextBlocks(ai.CompactionSummaryText(m.Summary))
}

func BashExecutionToText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case BashExecutionMessage:
		return ai.FormatBashExecutionText(m.Command, m.Output, m.ExitCode, m.Cancelled, m.Truncated, m.FullOutputPath)
	case *BashExecutionMessage:
		if m == nil {
			return ""
		}
		return ai.FormatBashExecutionText(m.Command, m.Output, m.ExitCode, m.Cancelled, m.Truncated, m.FullOutputPath)
	default:
		custom, _ := ai.AsCustomMessage(msg)
		return ai.FormatBashExecutionText(custom.Command, custom.Output, custom.ExitCode, custom.Cancelled, custom.Truncated, custom.FullOutputPath)
	}
}

func ConvertToLLM(messages []agent.AgentMessage) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		if converted, ok := convertKnownHarnessMessage(message); ok {
			out = append(out, converted...)
			continue
		}
		if converted, ok := ai.NormalizeProviderContentMessage(message); ok {
			out = append(out, converted...)
			continue
		}
		switch m := message.(type) {
		case ai.UserMessage, *ai.UserMessage, ai.AssistantMessage, *ai.AssistantMessage, ai.ToolResultMessage, *ai.ToolResultMessage:
			out = append(out, message)
		case ai.CustomMessage:
			out = append(out, convertCustomAIMessage(m)...)
		case *ai.CustomMessage:
			if m != nil {
				out = append(out, convertCustomAIMessage(*m)...)
			}
		}
	}
	return out, nil
}

func convertCustomAIMessage(custom ai.CustomMessage) []ai.Message {
	switch custom.MessageRole() {
	case "bashExecution":
		if custom.ExcludeFromContext {
			return nil
		}
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(BashExecutionToText(custom)), TimestampMs: custom.Timestamp()}}
	case "custom":
		if blocks, ok := ai.CustomContentBlocks(custom.Content); ok && len(blocks) > 0 {
			return []ai.Message{ai.UserMessage{Role: "user", Content: blocks, TimestampMs: custom.Timestamp()}}
		}
	case "branchSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(ai.BranchSummaryText(custom.Summary)), TimestampMs: custom.Timestamp()}}
	case "compactionSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(ai.CompactionSummaryText(custom.Summary)), TimestampMs: custom.Timestamp()}}
	}
	return nil
}

func convertKnownHarnessMessage(message agent.AgentMessage) ([]ai.Message, bool) {
	switch m := message.(type) {
	case BashExecutionMessage:
		if m.ExcludeFromContext {
			return nil, true
		}
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(BashExecutionToText(m)), TimestampMs: m.Timestamp()}}, true
	case *BashExecutionMessage:
		if m == nil || m.ExcludeFromContext {
			return nil, true
		}
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(BashExecutionToText(m)), TimestampMs: m.Timestamp()}}, true
	case CustomMessage:
		if blocks, ok := ai.CustomContentBlocks(m.Content); ok && len(blocks) > 0 {
			return []ai.Message{ai.UserMessage{Role: "user", Content: blocks, TimestampMs: m.Timestamp()}}, true
		}
		return nil, true
	case *CustomMessage:
		if m == nil {
			return nil, true
		}
		if blocks, ok := ai.CustomContentBlocks(m.Content); ok && len(blocks) > 0 {
			return []ai.Message{ai.UserMessage{Role: "user", Content: blocks, TimestampMs: m.Timestamp()}}, true
		}
		return nil, true
	case BranchSummaryMessage:
		return []ai.Message{ai.UserMessage{Role: "user", Content: m.ContentBlocks(), TimestampMs: m.Timestamp()}}, true
	case *BranchSummaryMessage:
		if m == nil {
			return nil, true
		}
		return []ai.Message{ai.UserMessage{Role: "user", Content: m.ContentBlocks(), TimestampMs: m.Timestamp()}}, true
	case CompactionSummaryMessage:
		return []ai.Message{ai.UserMessage{Role: "user", Content: m.ContentBlocks(), TimestampMs: m.Timestamp()}}, true
	case *CompactionSummaryMessage:
		if m == nil {
			return nil, true
		}
		return []ai.Message{ai.UserMessage{Role: "user", Content: m.ContentBlocks(), TimestampMs: m.Timestamp()}}, true
	default:
		return nil, false
	}
}

func CreateBranchSummaryMessage(summary, fromID, timestamp string) agent.AgentMessage {
	return BranchSummaryMessage{
		Role:        "branchSummary",
		Summary:     summary,
		FromID:      fromID,
		TimestampMs: parseMessageTimestamp(timestamp),
	}
}

func CreateCompactionSummaryMessage(summary string, tokensBefore int, timestamp string) agent.AgentMessage {
	return CompactionSummaryMessage{
		Role:         "compactionSummary",
		Summary:      summary,
		TokensBefore: tokensBefore,
		TimestampMs:  parseMessageTimestamp(timestamp),
	}
}

func CreateCustomMessage(customType string, content any, display bool, details any, timestamp string) agent.AgentMessage {
	return CustomMessage{
		Role:        "custom",
		CustomType:  customType,
		Content:     content,
		Display:     display,
		Details:     details,
		TimestampMs: parseMessageTimestamp(timestamp),
	}
}

func parseMessageTimestamp(timestamp string) int64 {
	if timestamp == "" {
		return time.Now().UnixMilli()
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return parsed.UnixMilli()
}
