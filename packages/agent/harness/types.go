package harness

import (
	"context"

	"github.com/guanshan/pi-go/packages/agent"
	harnesscompaction "github.com/guanshan/pi-go/packages/agent/harness/compaction"
	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type Skill struct {
	Name                   string
	Description            string
	Content                string
	FilePath               string
	DisableModelInvocation bool
}

type SkillDiagnostic struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Source  any    `json:"source,omitempty"`
}

type SkillLoadResult struct {
	Skills      []Skill
	Diagnostics []SkillDiagnostic
}

type SourcedSkillInput struct {
	Path   string
	Source any
}

type SourcedSkill struct {
	Skill  Skill
	Source any
}

type SourcedSkillLoadResult struct {
	Skills      []SourcedSkill
	Diagnostics []SkillDiagnostic
}

type PromptTemplate struct {
	Name        string
	Description string
	Content     string
}

type PromptTemplateDiagnostic struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path"`
	Source  any    `json:"source,omitempty"`
}

type PromptTemplateLoadResult struct {
	PromptTemplates []PromptTemplate
	Diagnostics     []PromptTemplateDiagnostic
}

type SourcedPromptTemplateInput struct {
	Path   string
	Source any
}

type SourcedPromptTemplate struct {
	PromptTemplate PromptTemplate
	Source         any
}

type SourcedPromptTemplateLoadResult struct {
	PromptTemplates []SourcedPromptTemplate
	Diagnostics     []PromptTemplateDiagnostic
}

type Resources struct {
	Skills          []Skill
	PromptTemplates []PromptTemplate
}

type StreamOptions struct {
	Transport       string
	TimeoutMs       int
	IdleTimeoutMs   int
	MaxRetries      int
	MaxRetryDelayMs int
	Headers         map[string]string
	Metadata        map[string]any
	CacheRetention  string
}

type StreamOptionsPatch struct {
	Transport       *string
	TimeoutMs       *int
	IdleTimeoutMs   *int
	MaxRetries      *int
	MaxRetryDelayMs *int
	CacheRetention  *string
	Headers         map[string]*string
	HeadersUnset    bool
	Metadata        map[string]*AnyValue
	MetadataUnset   bool
}

type AnyValue struct {
	V any
}

type Phase string

const (
	PhaseIdle          Phase = "idle"
	PhaseTurn          Phase = "turn"
	PhaseCompaction    Phase = "compaction"
	PhaseBranchSummary Phase = "branch_summary"
	PhaseRetry         Phase = "retry"
)

type Options struct {
	Env                 harnessenv.ExecutionEnv
	Session             *session.Session
	Registry            *ai.ModelRegistry
	StreamFn            agent.StreamFn
	Tools               []agent.AgentTool
	Resources           Resources
	SystemPrompt        SystemPromptSource
	GetAPIKeyAndHeaders APIKeyResolver
	StreamOptions       StreamOptions
	Model               ai.Model
	ThinkingLevel       ai.ThinkingLevel
	ActiveToolNames     []string
	SteeringMode        agent.QueueMode
	FollowUpMode        agent.QueueMode
}

type APIKeyResolver func(ctx context.Context, model ai.Model) (APIKeyAndHeaders, error)

type APIKeyAndHeaders struct {
	APIKey  string
	Headers map[string]string
}

type PromptOptions struct {
	Images []ai.ContentBlock
}

type AbortResult struct {
	Aborted bool
}

type NavigateTreeOptions struct {
	Summary             string
	Details             any
	CustomInstructions  string
	ReplaceInstructions bool
	UserWantsSummary    bool
	Label               string
	SkipSummary         bool
	GeneratedFromHook   bool
}

type NavigateTreeResult struct {
	OldLeafID    string
	NewLeafID    string
	EditorText   string
	SummaryEntry *session.BranchSummaryEntry
	Canceled     bool
}

type BranchSummary = harnesscompaction.BranchSummary
type TreePreparation = harnesscompaction.TreePreparation
