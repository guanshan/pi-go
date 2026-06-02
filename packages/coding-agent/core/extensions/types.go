package extensions

import (
	"context"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

type CommandInfo struct {
	Name        string
	Description string
	Source      string
	Execute     func(context.Context, string) (string, error) `json:"-"`
}

// FlagDefinition is a CLI flag declared by an extension (mirroring the upstream
// ExtensionFlag). Type is "boolean" or "string"; Default is the value used when
// the flag is not supplied on the command line.
type FlagDefinition struct {
	Name        string
	Description string
	Type        string
	Default     any
}

type ToolDefinition struct {
	Name        string
	Label       string
	Description string
	Parameters  map[string]any
	Execute     func(context.Context, []byte) (ai.ToolResult, error)
}

func DefineTool(name, description string, parameters map[string]any, execute func(context.Context, []byte) (ai.ToolResult, error)) ToolDefinition {
	return ToolDefinition{Name: name, Label: name, Description: description, Parameters: parameters, Execute: execute}
}

type SessionStartReason string

const (
	SessionStartStartup SessionStartReason = "startup"
	SessionStartReload  SessionStartReason = "reload"
	SessionStartNew     SessionStartReason = "new"
	SessionStartResume  SessionStartReason = "resume"
	SessionStartFork    SessionStartReason = "fork"
)

type SessionStartEvent struct {
	Type                string
	Reason              SessionStartReason
	PreviousSessionFile string
}

type SessionBeforeSwitchEvent struct {
	Type              string
	Reason            SessionStartReason
	TargetSessionFile string
	Cancel            bool
}

type SessionBeforeForkEvent struct {
	Type     string
	EntryID  string
	Position string
	Cancel   bool
}

type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          any
}

type SessionBeforeCompactEvent struct {
	Type               string
	Preparation        any
	BranchEntries      any
	CustomInstructions string
	Signal             context.Context
	Cancel             bool
	Result             *CompactionResult
}

type SessionCompactEvent struct {
	Type            string
	CompactionEntry any
	FromExtension   bool
}

type SessionShutdownReason string

const (
	SessionShutdownQuit   SessionShutdownReason = "quit"
	SessionShutdownReload SessionShutdownReason = "reload"
	SessionShutdownNew    SessionShutdownReason = "new"
	SessionShutdownResume SessionShutdownReason = "resume"
	SessionShutdownFork   SessionShutdownReason = "fork"
)

type SessionShutdownEvent struct {
	Type              string
	Reason            SessionShutdownReason
	TargetSessionFile string
}

type BranchSummary struct {
	Summary string
	Details any
}

type TreePreparation struct {
	TargetID            string
	OldLeafID           string
	CommonAncestorID    string
	EntriesToSummarize  any
	UserWantsSummary    bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}

type SessionBeforeTreeEvent struct {
	Type                string
	Preparation         TreePreparation
	Signal              context.Context
	Cancel              bool
	Summary             *BranchSummary
	CustomInstructions  *string
	ReplaceInstructions *bool
	Label               *string
}

type SessionTreeEvent struct {
	Type          string
	NewLeafID     string
	OldLeafID     string
	SummaryEntry  any
	FromExtension bool
}

type AgentStartEvent struct {
	Type string
}

type AgentEndEvent struct {
	Type     string
	Messages []agentcore.AgentMessage
}

type TurnStartEvent struct {
	Type      string
	TurnIndex int
	Timestamp int64
}

type TurnEndEvent struct {
	Type        string
	TurnIndex   int
	Message     agentcore.AgentMessage
	ToolResults []ai.ToolResultMessage
}

type MessageStartEvent struct {
	Type    string
	Message agentcore.AgentMessage
}

type MessageUpdateEvent struct {
	Type                  string
	Message               agentcore.AgentMessage
	AssistantMessageEvent ai.AssistantMessageEvent
}

type MessageEndEvent struct {
	Type    string
	Message agentcore.AgentMessage
}

// ToolCallEvent mirrors the TS ToolCallEvent (tool_call). The JSON shape matches
// the TS API surface so script handlers receive { toolCallId, toolName, input }.
// Input is the decoded tool arguments and is mutable: a script handler may mutate
// it in place to patch the arguments before execution. Block/Reason carry the
// handler's decision back to the caller (the script bridge writes the merged
// payload back, so Object.assign({block,reason}) round-trips through the JSON).
type ToolCallEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Input      any    `json:"input"`
	Block      bool   `json:"block,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ToolResultEvent mirrors the TS ToolResultEvent (tool_result). The JSON shape is
// { toolCallId, toolName, input, content, details, isError }. A script handler can
// override Content/Details/IsError; the bridge merges its returned result onto the
// payload, so these fields carry the overridden values back to the caller.
type ToolResultEvent struct {
	Type       string            `json:"type"`
	ToolCallID string            `json:"toolCallId"`
	ToolName   string            `json:"toolName"`
	Input      any               `json:"input"`
	Content    []ai.ContentBlock `json:"content"`
	Details    any               `json:"details"`
	IsError    bool              `json:"isError"`
}
