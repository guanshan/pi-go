package tools

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/guanshan/pi-go/packages/ai"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024
	GrepMaxLineLength = 500
	DefaultGrepLimit  = 100
	DefaultFindLimit  = 1000
	DefaultLsLimit    = 500
)

type TruncationResult struct {
	Content               string `json:"content"`
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncatedBy,omitempty"`
	TotalLines            int    `json:"totalLines"`
	TotalBytes            int    `json:"totalBytes"`
	OutputLines           int    `json:"outputLines"`
	OutputBytes           int    `json:"outputBytes"`
	LastLinePartial       bool   `json:"lastLinePartial"`
	FirstLineExceedsLimit bool   `json:"firstLineExceedsLimit"`
	MaxLines              int    `json:"maxLines"`
	MaxBytes              int    `json:"maxBytes"`
}

type ReadTool struct {
	CWD string
	// AutoResize mirrors the imageAutoResize setting: when true, image reads are
	// shrunk to fit the inline image size limit (and omitted if impossible).
	AutoResize bool
}
type WriteTool struct{ CWD string }
type EditTool struct{ CWD string }
type BashTool struct {
	CWD           string
	ShellPath     string
	CommandPrefix string
	// BinDir is the agent bin directory prepended to PATH for every command,
	// matching getShellEnv() in TS. Passed in from the core package to avoid an
	// import cycle (core/tools must not import core). Empty leaves PATH as-is.
	BinDir string
}
type GrepTool struct {
	CWD string
	// BinDir is the agent bin directory searched for a vendored rg binary before
	// falling back to PATH. Empty consults PATH only.
	BinDir string
}
type FindTool struct{ CWD string }
type LsTool struct{ CWD string }

type BuiltinToolOptions struct {
	ShellPath     string
	CommandPrefix string
	AutoResize    bool
	// BinDir is the agent bin directory the bash tool prepends to PATH.
	BinDir string
}

type FilterOptions struct {
	NoTools        bool
	Tools          []string
	NoBuiltinTools bool
}

// ToolUpdate reports an incremental (partial) tool result while a tool is still
// running. It mirrors the agent framework's ToolUpdateCallback and lets
// long-running tools (e.g. bash) stream output so the harness can emit
// ToolExecutionUpdateEvent. It may be nil when the caller does not want
// incremental updates.
type ToolUpdate func(partial ai.ToolResult)

type RuntimeTool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Execute(context.Context, json.RawMessage, ToolUpdate) ai.ToolResult
}

type ToolSet map[string]RuntimeTool

func (tools ToolSet) AITools() ai.ToolSet {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ai.ToolSet{}
	for _, name := range names {
		tool := tools[name]
		if tool == nil {
			continue
		}
		toolName := tool.Name()
		if toolName == "" {
			toolName = name
		}
		out[toolName] = ai.Tool{Name: toolName, Description: tool.Description(), Parameters: tool.Schema()}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
