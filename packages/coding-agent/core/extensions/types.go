package extensions

import (
	"context"
	"encoding/json"

	agentcore "github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

type CommandInfo struct {
	Name        string
	Description string
	Source      string
	// InvocationName is the deduplicated name used to invoke the command on the
	// wire: when two extensions register the same command name, the second
	// becomes "name:2", etc. (mirrors TS runner getRegisteredCommands). Populated
	// by Runner.RegisteredCommands; equals Name when there is no collision.
	InvocationName string
	// SourceInfo carries the command's origin (e.g. {"path": <extension file>}),
	// mirroring TS ResolvedCommand.sourceInfo. Nil for commands registered without
	// a known source path.
	SourceInfo map[string]any
	Execute    func(context.Context, string) (string, error) `json:"-"`
}

type ShortcutDefinition struct {
	Key         string
	Description string
	Source      string
	Execute     func(context.Context) error `json:"-"`
}

type AutocompleteItem struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Provider    int    `json:"-"`
	ProviderID  uint64 `json:"-"`
	Source      string `json:"-"`
	SourceIndex int    `json:"-"`
}

type AutocompleteSuggestions struct {
	Items  []AutocompleteItem `json:"items"`
	Prefix string             `json:"prefix,omitempty"`
}

type AutocompleteRequest struct {
	Lines      []string `json:"lines,omitempty"`
	CursorLine int      `json:"cursorLine"`
	CursorCol  int      `json:"cursorCol"`
	Input      string   `json:"input,omitempty"`
	Cursor     int      `json:"cursor,omitempty"`
	Force      bool     `json:"force,omitempty"`
}

type AutocompleteApplyRequest struct {
	Lines      []string         `json:"lines,omitempty"`
	CursorLine int              `json:"cursorLine"`
	CursorCol  int              `json:"cursorCol"`
	Input      string           `json:"input,omitempty"`
	Cursor     int              `json:"cursor,omitempty"`
	Item       AutocompleteItem `json:"item"`
	Prefix     string           `json:"prefix,omitempty"`
}

type AutocompleteApplyResult struct {
	Lines      []string `json:"lines,omitempty"`
	CursorLine int      `json:"cursorLine"`
	CursorCol  int      `json:"cursorCol"`
	Input      string   `json:"input,omitempty"`
	Cursor     int      `json:"cursor,omitempty"`
}

type AutocompleteProviderDefinition struct {
	ID      uint64 `json:"-"`
	Source  string
	Suggest func(context.Context, AutocompleteRequest) (AutocompleteSuggestions, error)      `json:"-"`
	Apply   func(context.Context, AutocompleteApplyRequest) (AutocompleteApplyResult, error) `json:"-"`
}

type ProviderDefinition struct {
	API          string
	ProviderName string
	Source       string
	Provider     ai.Provider     `json:"-"`
	ModelConfig  json.RawMessage `json:"-"`
	// OAuth is the extension provider's serializable OAuth login descriptor (TS
	// registerProvider `oauth`); HasModifyModels reports whether it defined a
	// modifyModels callback. Recorded for parity (X-02); the host does not yet
	// wire ext OAuth into /login or invoke modifyModels (documented partial).
	OAuth           json.RawMessage `json:"-"`
	HasModifyModels bool            `json:"-"`
}

type MessageRenderRequest struct {
	CustomType  string `json:"customType"`
	Content     any    `json:"content,omitempty"`
	Display     bool   `json:"display"`
	Details     any    `json:"details,omitempty"`
	Expanded    bool   `json:"expanded"`
	Width       int    `json:"width,omitempty"`
	TimestampMs int64  `json:"timestamp,omitempty"`
}

type MessageRenderResult struct {
	Lines []string `json:"lines,omitempty"`
}

type MessageRendererDefinition struct {
	CustomType string
	Source     string
	Render     func(context.Context, MessageRenderRequest) (MessageRenderResult, error) `json:"-"`
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

// ExtensionContextSnapshot is the host-backed state exposed to script
// extensions as ExtensionContext. The bridge sends a fresh snapshot with each
// host-initiated request so ctx values mirror the live session instead of the
// compatibility-only defaults used before the host is bound.
type ExtensionContextSnapshot struct {
	CWD                string     `json:"cwd,omitempty"`
	Mode               string     `json:"mode,omitempty"`
	HasUI              bool       `json:"hasUI"`
	Model              *ai.Model  `json:"model,omitempty"`
	Models             []ai.Model `json:"models,omitempty"`
	AvailableModels    []ai.Model `json:"availableModels,omitempty"`
	IsIdle             bool       `json:"isIdle"`
	HasPendingMessages bool       `json:"hasPendingMessages"`
	SystemPrompt       string     `json:"systemPrompt,omitempty"`
	SessionID          string     `json:"sessionId,omitempty"`
	SessionFile        string     `json:"sessionFile,omitempty"`
	Entries            any        `json:"entries,omitempty"`
	BranchEntries      any        `json:"branchEntries,omitempty"`
	LeafID             string     `json:"leafId,omitempty"`
	ContextUsage       any        `json:"contextUsage,omitempty"`
}

type ExtensionContextProvider func() ExtensionContextSnapshot

type ExtensionContextAction struct {
	Name   string
	Params json.RawMessage
}

type ExtensionContextActionHandler func(context.Context, ExtensionContextAction) (json.RawMessage, error)

type ToolDefinition struct {
	Name        string
	Label       string
	Description string
	Parameters  map[string]any
	Execute     func(context.Context, []byte) (ai.ToolResult, error)

	// PromptSnippet is an optional one-line snippet for the Available tools
	// section in the default system prompt. Custom tools are omitted from that
	// section when this is empty (TS ToolDefinition.promptSnippet). Serializable
	// metadata forwarded end-to-end from the script bridge.
	PromptSnippet string
	// PromptGuidelines are guideline bullets appended to the default system
	// prompt Guidelines section when this tool is active
	// (TS ToolDefinition.promptGuidelines). Serializable metadata.
	PromptGuidelines []string
	// RenderShell controls whether the standard colored shell is rendered or the
	// tool renders its own framing ("default" | "self";
	// TS ToolDefinition.renderShell). Serializable metadata.
	RenderShell string
	// ExecutionMode is the per-tool execution-mode override ("sequential" |
	// "parallel"; TS ToolDefinition.executionMode). Serializable metadata.
	ExecutionMode agentcore.ToolExecutionMode

	// PrepareArguments, RenderCall, and RenderResult mirror the function-valued
	// TS ToolDefinition fields. They are populated for native (in-process) Go
	// extensions; script (Node) extensions cannot pass functions across the
	// process boundary, so these stay nil for script-sourced tools (the
	// serializable metadata above is still plumbed through). RenderCall/
	// RenderResult are host-consumed (tool render display); they are typed as
	// generic funcs to avoid a tui dependency in this package.
	PrepareArguments func(context.Context, []byte) ([]byte, error) `json:"-"`
	RenderCall       any                                           `json:"-"`
	RenderResult     any                                           `json:"-"`
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
