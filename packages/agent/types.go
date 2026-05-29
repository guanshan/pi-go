// Package agent provides the Go port of @earendil-works/pi-agent-core.
package agent

import (
	"context"
	"encoding/json"

	"github.com/guanshan/pi-go/packages/ai"
)

type ThinkingLevel = ai.ThinkingLevel
type AgentMessage = ai.Message

// AgentToolResult is the final or partial result produced by a tool.
//
// IsError is a deliberate Go enhancement over the TypeScript source. In the TS
// implementation (src/types.ts / agent-loop.ts) tools cannot report failure in
// their return value: executePreparedToolCall always returns isError:false on a
// normal return, and the only way to signal an error is to throw. Go tools
// instead return (AgentToolResult, error) and may also set IsError on a normally
// returned result to flag a "soft" error (a failure the tool handled itself but
// still wants surfaced to the model/UI as an error).
//
// Both failure paths converge on the same finalization (see tool_exec.go):
//   - A thrown failure (Execute returns a non-nil error, or panics) becomes an
//     error tool result built by createErrorToolResult, i.e. Details == {} and
//     Terminate == false.
//   - A soft error (Execute returns (result, nil) with result.IsError == true)
//     keeps the Details and Terminate the tool put on the result.
//
// In every other respect the two paths are identical: the AfterToolCall hook
// observes the same isError flag, Terminate still participates in batch
// termination (shouldTerminateToolBatch), and Details flows unchanged into the
// emitted ToolResultMessage. Tools that want the TS semantics simply leave
// IsError false and return an error to fail.
type AgentToolResult struct {
	Content   []ai.ContentBlock `json:"content"`
	Details   any               `json:"details,omitempty"`
	IsError   bool              `json:"isError,omitempty"`
	Terminate bool              `json:"terminate,omitempty"`
}

type ToolUpdateCallback func(partial AgentToolResult)

type AgentTool interface {
	Name() string
	Label() string
	Description() string
	Schema() map[string]any
	// Execute runs the tool call. Returning a non-nil error fails the call the
	// same way a thrown error does in the TypeScript source. Alternatively a
	// tool may return (result, nil) with result.IsError == true to report a soft
	// error while still supplying Content/Details/Terminate; see AgentToolResult.
	Execute(context.Context, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error)
}

type ToolExecutionModeProvider interface {
	ToolExecutionMode() ToolExecutionMode
}

type PrepareArgumentsProvider interface {
	PrepareArguments(json.RawMessage) (json.RawMessage, error)
}

type QueueMode string

const (
	QueueAll        QueueMode = "all"
	QueueOneAtATime QueueMode = "one-at-a-time"
	// QueueOneAtTime is a misspelled alias kept for the coding-agent package.
	//
	// Deprecated: use QueueOneAtATime. The two are identical; QueueOneAtATime is
	// the canonical spelling.
	QueueOneAtTime = QueueOneAtATime
)

type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

type AssistantStream interface {
	Events() <-chan ai.AssistantMessageEvent
	Result() ai.AssistantMessage
}

type StreamFn func(context.Context, ai.Model, ai.Context, ai.StreamOptions) AssistantStream

type ConvertToLLMFunc func(messages []AgentMessage) ([]ai.Message, error)
type TransformContextFunc func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
type GetAPIKeyFunc func(provider string) (string, error)
type BeforeToolCallFunc func(ctx context.Context, tc BeforeToolCallContext) (BeforeToolCallResult, error)
type AfterToolCallFunc func(ctx context.Context, tc AfterToolCallContext) (AfterToolCallResult, error)
type ShouldStopAfterTurnFunc func(ctx context.Context, tc ShouldStopAfterTurnContext) (bool, error)
type PrepareNextTurnFunc func(ctx context.Context, tc PrepareNextTurnContext) (*AgentLoopTurnUpdate, error)

type BeforeToolCallResult struct {
	Block  bool   `json:"block,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// AfterToolCallResult is a partial override returned from AfterToolCall. It
// mirrors the field-by-field "?? " merge semantics of the TypeScript
// finalizeExecutedToolCall: a field overrides the executed tool result only when
// it was explicitly provided, otherwise the original value is kept.
//
// Go has no undefined, so "explicitly provided" is encoded separately from the
// zero value:
//   - HasContent gates Content. When true, Content replaces the result content
//     in full (including an explicit empty/nil slice to clear it). When false,
//     Content is ignored and the original content is kept. This distinguishes an
//     intentional "clear the content" override from "don't touch content",
//     which a bare nil slice cannot.
//   - HasDetails gates Details with the same semantics.
//   - IsError and Terminate are pointers: non-nil overrides, nil keeps original.
//
// No deep merge is performed for Content or Details.
type AfterToolCallResult struct {
	Content    []ai.ContentBlock `json:"content,omitempty"`
	HasContent bool              `json:"-"`
	Details    any               `json:"details,omitempty"`
	HasDetails bool              `json:"-"`
	IsError    *bool             `json:"isError,omitempty"`
	Terminate  *bool             `json:"terminate,omitempty"`
}

type BeforeToolCallContext struct {
	AssistantMessage ai.AssistantMessage
	ToolCall         ai.ToolCall
	Args             any
	Context          AgentContext
}

type AfterToolCallContext struct {
	AssistantMessage ai.AssistantMessage
	ToolCall         ai.ToolCall
	Args             any
	Result           AgentToolResult
	IsError          bool
	Context          AgentContext
}

type ShouldStopAfterTurnContext struct {
	Message     ai.AssistantMessage
	ToolResults []ai.ToolResultMessage
	Context     AgentContext
	NewMessages []AgentMessage
}

type PrepareNextTurnContext = ShouldStopAfterTurnContext

type AgentLoopTurnUpdate struct {
	Context       *AgentContext
	Model         *ai.Model
	ThinkingLevel *ThinkingLevel
}

type AgentLoopConfig struct {
	Model            ai.Model
	Reasoning        ai.ThinkingLevel
	SessionID        string
	Transport        string
	ThinkingBudgets  *ai.ThinkingBudgets
	MaxRetryDelayMs  int
	ConvertToLLM     ConvertToLLMFunc
	TransformContext TransformContextFunc
	GetAPIKey        GetAPIKeyFunc
	BeforeToolCall   BeforeToolCallFunc
	AfterToolCall    AfterToolCallFunc

	ShouldStopAfterTurn ShouldStopAfterTurnFunc
	PrepareNextTurn     PrepareNextTurnFunc

	GetSteeringMessages func(context.Context) ([]AgentMessage, error)
	GetFollowUpMessages func(context.Context) ([]AgentMessage, error)

	ToolExecution ToolExecutionMode
	EventBuffer   int

	OnPayload  func(payload any, model ai.Model) (any, error)
	OnResponse func(resp ai.ProviderResponse, model ai.Model) error
}

type AgentOptions struct {
	InitialState AgentState
	Registry     *ai.ModelRegistry
	StreamFn     StreamFn
	ConvertToLLM ConvertToLLMFunc

	TransformContext TransformContextFunc
	GetAPIKey        GetAPIKeyFunc
	BeforeToolCall   BeforeToolCallFunc
	AfterToolCall    AfterToolCallFunc

	ShouldStopAfterTurn ShouldStopAfterTurnFunc
	PrepareNextTurn     PrepareNextTurnFunc

	SteeringMode  QueueMode
	FollowUpMode  QueueMode
	ToolExecution ToolExecutionMode

	SessionID       string
	ThinkingBudgets *ai.ThinkingBudgets
	Transport       string
	MaxRetryDelayMs int
	OnPayload       func(payload any, model ai.Model) (any, error)
	OnResponse      func(resp ai.ProviderResponse, model ai.Model) error
}
