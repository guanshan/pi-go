package compaction

import (
	"context"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type BranchPreparation struct {
	Messages    []agent.AgentMessage
	FileOps     FileOperations
	TotalTokens int
}

type CollectEntriesResult struct {
	Entries          []session.Entry
	CommonAncestorID *string
}

type BranchSummary struct {
	Summary       string
	ReadFiles     []string
	ModifiedFiles []string
}

type BranchSummaryOptions struct {
	Model               ai.Model
	APIKey              string
	Headers             map[string]string
	CustomInstructions  string
	ReplaceInstructions bool
	ReserveTokens       int
	Registry            *ai.ModelRegistry
}

type TreePreparation struct {
	TargetID            string
	OldLeafID           *string
	CommonAncestorID    *string
	EntriesToSummarize  []session.Entry
	UserWantsSummary    bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}

func CollectEntriesForBranchSummary(ctx context.Context, sess *session.Session, oldLeafID *string, targetID string) (CollectEntriesResult, error) {
	if oldLeafID == nil || *oldLeafID == "" {
		return CollectEntriesResult{}, nil
	}
	oldBranch, err := sess.Branch(ctx, oldLeafID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	targetBranch, err := sess.Branch(ctx, &targetID)
	if err != nil {
		return CollectEntriesResult{}, err
	}
	common := commonAncestorID(oldBranch, targetBranch)
	start := 0
	if common != nil {
		for i, entry := range oldBranch {
			if entry.EntryID() == *common {
				start = i + 1
				break
			}
		}
	}
	return CollectEntriesResult{Entries: append([]session.Entry(nil), oldBranch[start:]...), CommonAncestorID: common}, nil
}

func PrepareBranchEntries(entries []session.Entry, tokenBudget int) BranchPreparation {
	messages := []agent.AgentMessage{}
	fileOps := CreateFileOps()
	totalTokens := 0
	for _, entry := range entries {
		if branch, ok := entry.(session.BranchSummaryEntry); ok && !branch.FromHook {
			mergeCompactionDetails(&fileOps, branch.Details)
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		message, ok := messageFromEntry(entries[i])
		if !ok {
			continue
		}
		ExtractFileOpsFromMessage(message, &fileOps)
		tokens := estimateMessageTokens(message)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			// Mirror TS branch-summarization.ts:149-156: a compaction or
			// branch_summary entry that would exceed the budget is still
			// included when we are below 90% of the budget, so the carried
			// summary is not dropped.
			if isCompactionOrBranchSummary(entries[i]) && float64(totalTokens) < float64(tokenBudget)*0.9 {
				messages = append([]agent.AgentMessage{message}, messages...)
				totalTokens += tokens
			}
			break
		}
		messages = append([]agent.AgentMessage{message}, messages...)
		totalTokens += tokens
	}
	return BranchPreparation{Messages: messages, FileOps: fileOps, TotalTokens: totalTokens}
}

func GenerateBranchSummary(ctx context.Context, entries []session.Entry, options BranchSummaryOptions) (BranchSummary, error) {
	reserveTokens := options.ReserveTokens
	if reserveTokens <= 0 {
		reserveTokens = DefaultSettings.ReserveTokens
	}
	// Mirror TS branch-summarization.ts:206 `const contextWindow = model.contextWindow || 128000`.
	contextWindow := options.Model.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	tokenBudget := contextWindow - reserveTokens
	prepared := PrepareBranchEntries(entries, tokenBudget)
	readFiles, modifiedFiles := ComputeFileLists(prepared.FileOps)
	if len(prepared.Messages) == 0 {
		return BranchSummary{Summary: "No content to summarize", ReadFiles: readFiles, ModifiedFiles: modifiedFiles}, nil
	}
	llmMessages, err := messagesToLLM(prepared.Messages)
	if err != nil {
		return BranchSummary{}, err
	}
	instructions := branchSummaryPrompt
	if custom := strings.TrimSpace(options.CustomInstructions); custom != "" {
		if options.ReplaceInstructions {
			instructions = custom
		} else {
			instructions += "\n\nAdditional focus: " + custom
		}
	}
	prompt := "<conversation>\n" + SerializeConversation(llmMessages) + "\n</conversation>\n\n" + instructions
	summary := ""
	complete := ai.CompleteSimple
	if options.Registry != nil {
		complete = options.Registry.CompleteSimple
	}
	message, err := complete(ctx, options.Model, ai.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages:     []ai.Message{ai.NewUserMessage(prompt, nil)},
	}, ai.SimpleStreamOptions{APIKey: options.APIKey, Headers: options.Headers, MaxTokens: 2048})
	if err != nil {
		return BranchSummary{}, &BranchSummaryError{Code: "summarization_failed", Msg: "Branch summary failed", Err: err}
	}
	if message.StopReason == "aborted" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = "Branch summary aborted"
		}
		return BranchSummary{}, &BranchSummaryError{Code: "aborted", Msg: msg}
	}
	if message.StopReason == "error" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = "Unknown error"
		}
		return BranchSummary{}, &BranchSummaryError{Code: "summarization_failed", Msg: "Branch summary failed: " + msg}
	}
	// Mirror TS branch-summarization.ts:250-253: join text blocks with "\n".
	summary = summaryTextContent(message)
	if strings.TrimSpace(summary) == "" {
		summary = "No summary generated"
	}
	summary = branchSummaryPreamble + strings.TrimSpace(summary) + FormatFileOperations(readFiles, modifiedFiles)
	return BranchSummary{Summary: summary, ReadFiles: readFiles, ModifiedFiles: modifiedFiles}, nil
}

func isCompactionOrBranchSummary(entry session.Entry) bool {
	switch entry.(type) {
	case session.CompactionEntry, session.BranchSummaryEntry:
		return true
	default:
		return false
	}
}

func messageFromEntry(entry session.Entry) (agent.AgentMessage, bool) {
	switch e := entry.(type) {
	case session.MessageEntry:
		if e.Message == nil {
			return nil, false
		}
		if _, ok := ai.AsToolResultMessage(e.Message); ok {
			return nil, false
		}
		return e.Message, true
	case session.CustomMessageEntry:
		return createCustomMessage(e.CustomType, e.Content, e.Display, e.Details, e.EntryTimestamp()), true
	case session.BranchSummaryEntry:
		return createBranchSummaryMessage(e.Summary, e.FromID, e.EntryTimestamp()), true
	case session.CompactionEntry:
		return createCompactionSummaryMessage(e.Summary, e.TokensBefore, e.EntryTimestamp()), true
	default:
		return nil, false
	}
}

func messagesToLLM(messages []agent.AgentMessage) ([]ai.Message, error) {
	out := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch m := message.(type) {
		case ai.UserMessage, *ai.UserMessage, ai.AssistantMessage, *ai.AssistantMessage, ai.ToolResultMessage, *ai.ToolResultMessage:
			out = append(out, message)
		case ai.CustomMessage:
			out = append(out, convertCompactionCustomMessage(m)...)
		case *ai.CustomMessage:
			if m != nil {
				out = append(out, convertCompactionCustomMessage(*m)...)
			}
		}
	}
	return out, nil
}

func convertCompactionCustomMessage(custom ai.CustomMessage) []ai.Message {
	switch custom.MessageRole() {
	case "custom":
		switch content := custom.Content.(type) {
		case string:
			return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(content), TimestampMs: custom.Timestamp()}}
		case []ai.ContentBlock:
			return []ai.Message{ai.UserMessage{Role: "user", Content: content, TimestampMs: custom.Timestamp()}}
		}
	case "branchSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(branchSummaryPrefix + custom.Summary + branchSummarySuffix), TimestampMs: custom.Timestamp()}}
	case "compactionSummary":
		return []ai.Message{ai.UserMessage{Role: "user", Content: ai.TextBlocks(compactionSummaryPrefix + custom.Summary + compactionSummarySuffix), TimestampMs: custom.Timestamp()}}
	}
	return nil
}

func commonAncestorID(a, b []session.Entry) *string {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	var last string
	for i := 0; i < limit; i++ {
		if a[i].EntryID() == "" || a[i].EntryID() != b[i].EntryID() {
			break
		}
		last = a[i].EntryID()
	}
	if last == "" {
		return nil
	}
	return &last
}

const branchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

// branchSummaryPrompt mirrors TS BRANCH_SUMMARY_PROMPT (branch-summarization.ts:171-198).
const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const compactionSummaryPrefix = `The conversation history before this point was compacted into the following summary:

<summary>
`

const compactionSummarySuffix = `
</summary>`

const branchSummaryPrefix = `The following is a summary of a branch that this conversation came back from:

<summary>
`

const branchSummarySuffix = `</summary>`

func createBranchSummaryMessage(summary, fromID, timestamp string) agent.AgentMessage {
	return ai.CustomMessage{
		Role:        "branchSummary",
		Summary:     summary,
		FromID:      fromID,
		TimestampMs: parseMessageTimestamp(timestamp),
	}
}

func createCompactionSummaryMessage(summary string, tokensBefore int, timestamp string) agent.AgentMessage {
	return ai.CustomMessage{
		Role:         "compactionSummary",
		Summary:      summary,
		TokensBefore: tokensBefore,
		TimestampMs:  parseMessageTimestamp(timestamp),
	}
}

func createCustomMessage(customType string, content any, display bool, details any, timestamp string) agent.AgentMessage {
	return ai.CustomMessage{
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
