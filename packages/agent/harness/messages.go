package harness

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

const CompactionSummaryPrefix = `The conversation history before this point was compacted into the following summary:

<summary>
`

const CompactionSummarySuffix = `
</summary>`

const BranchSummaryPrefix = `The following is a summary of a branch that this conversation came back from:

<summary>
`

const BranchSummarySuffix = `</summary>`

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
	blocks, _ := customContentBlocks(m.Content)
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
	return ai.TextBlocks(BranchSummaryPrefix + m.Summary + BranchSummarySuffix)
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
	return ai.TextBlocks(CompactionSummaryPrefix + m.Summary + CompactionSummarySuffix)
}

func BashExecutionToText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case BashExecutionMessage:
		return formatBashExecution(m.Command, m.Output, m.ExitCode, m.Cancelled, m.Truncated, m.FullOutputPath)
	case *BashExecutionMessage:
		if m == nil {
			return ""
		}
		return formatBashExecution(m.Command, m.Output, m.ExitCode, m.Cancelled, m.Truncated, m.FullOutputPath)
	default:
		custom, _ := ai.AsCustomMessage(msg)
		return formatBashExecution(custom.Command, custom.Output, custom.ExitCode, custom.Cancelled, custom.Truncated, custom.FullOutputPath)
	}
}

func formatBashExecution(command string, output string, exitCode *int, cancelled bool, truncated bool, fullOutputPath string) string {
	text := fmt.Sprintf("Ran `%s`\n", command)
	if output != "" {
		text += "```\n" + output + "\n```"
	} else {
		text += "(no output)"
	}
	if cancelled {
		text += "\n\n(command cancelled)"
	} else if exitCode != nil && *exitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *exitCode)
	}
	if truncated && fullOutputPath != "" {
		text += "\n\n[Output truncated. Full output: " + fullOutputPath + "]"
	}
	return text
}

func ConvertToLLM(messages []agent.AgentMessage) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		if converted, ok := convertKnownHarnessMessage(message); ok {
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
		if blocks, ok := customContentBlocks(custom.Content); ok {
			return []ai.Message{ai.UserMessage{Role: "user", Content: blocks, TimestampMs: custom.Timestamp()}}
		}
	case "branchSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(BranchSummaryPrefix + custom.Summary + BranchSummarySuffix), TimestampMs: custom.Timestamp()}}
	case "compactionSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(CompactionSummaryPrefix + custom.Summary + CompactionSummarySuffix), TimestampMs: custom.Timestamp()}}
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
		if blocks, ok := customContentBlocks(m.Content); ok {
			return []ai.Message{ai.UserMessage{Role: "user", Content: blocks, TimestampMs: m.Timestamp()}}, true
		}
		return nil, true
	case *CustomMessage:
		if m == nil {
			return nil, true
		}
		if blocks, ok := customContentBlocks(m.Content); ok {
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

func customContentBlocks(content any) ([]ai.ContentBlock, bool) {
	switch value := content.(type) {
	case string:
		// TS: typeof content === "string" ? [{type:"text", text: content}] : content.
		return ai.TextBlocks(value), true
	case []ai.ContentBlock:
		return value, true
	case nil:
		// TS still emits a user message with the (empty/undefined) content array.
		return nil, true
	default:
		// After reloading a session from JSONL, an array content field decodes to
		// []interface{} (each element a map[string]any), not []ai.ContentBlock.
		// TS passes m.content through unchanged; the Go equivalent re-encodes the
		// decoded value and parses it into typed content blocks so text and image
		// blocks survive. This mirrors how ai.UnmarshalMessageJSON parses content.
		blocks, err := normalizeContentBlocks(value)
		if err != nil {
			return nil, false
		}
		return blocks, true
	}
}

// normalizeContentBlocks converts a JSON-decoded content value (typically
// []interface{} of map[string]any from a reloaded session) into typed
// ai.ContentBlock values by round-tripping through JSON.
func normalizeContentBlocks(value any) ([]ai.ContentBlock, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var blocks []ai.ContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
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
