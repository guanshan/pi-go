package compaction

import (
	"context"
	"math"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type Preparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []agent.AgentMessage
	TurnPrefixMessages  []agent.AgentMessage
	KeptMessages        []agent.AgentMessage
	IsSplitTurn         bool
	TokensBefore        int
	PreviousSummary     string
	FileOps             FileOperations
	Settings            Settings
}

type Result struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          any
	KeptMessages     []agent.AgentMessage
	TokensAfter      int
	MessagesBefore   int
}

func PrepareCompaction(branch []session.Entry, settings Settings) (*Preparation, error) {
	settings = withDefaults(settings)
	if !settings.Enabled {
		return nil, nil
	}
	if len(branch) == 0 {
		return nil, nil
	}
	if _, ok := branch[len(branch)-1].(session.CompactionEntry); ok {
		return nil, nil
	}
	var previousSummary string
	fileOpsFromDetailsTarget := CreateFileOps()
	boundaryStart := 0
	for i := len(branch) - 1; i >= 0; i-- {
		entry := branch[i]
		if compaction, ok := entry.(session.CompactionEntry); ok {
			previousSummary = compaction.Summary
			if !compaction.FromHook {
				mergeCompactionDetails(&fileOpsFromDetailsTarget, compaction.Details)
			}
			boundaryStart = i + 1
			for j, candidate := range branch {
				if candidate.EntryID() == compaction.FirstKeptEntryID {
					boundaryStart = j
					break
				}
			}
			break
		}
	}
	contextMessages := session.BuildContextFromEntries(branch).Messages
	if len(contextMessages) == 0 {
		return nil, nil
	}
	cutPoint := FindCutPoint(branch, boundaryStart, len(branch), settings.KeepRecentTokens)
	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(branch) || branch[cutPoint.FirstKeptEntryIndex].EntryID() == "" {
		return nil, &CompactionError{Code: "invalid_session", Msg: "first kept entry has no id"}
	}
	firstKeptEntryID := branch[cutPoint.FirstKeptEntryIndex].EntryID()
	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}
	messagesToSummarize := messagesFromEntries(branch, boundaryStart, historyEnd)
	turnPrefixMessages := []agent.AgentMessage(nil)
	if cutPoint.IsSplitTurn {
		turnPrefixMessages = messagesFromEntries(branch, cutPoint.TurnStartIndex, cutPoint.FirstKeptEntryIndex)
	}
	keptMessages := messagesFromEntries(branch, cutPoint.FirstKeptEntryIndex, len(branch))
	fileOps := CreateFileOps()
	for file := range fileOpsFromDetailsTarget.Read {
		fileOps.Read[file] = struct{}{}
	}
	for file := range fileOpsFromDetailsTarget.Edited {
		fileOps.Edited[file] = struct{}{}
	}
	for file := range fileOpsFromDetailsTarget.Written {
		fileOps.Written[file] = struct{}{}
	}
	for _, message := range messagesToSummarize {
		ExtractFileOpsFromMessage(message, &fileOps)
	}
	for _, message := range turnPrefixMessages {
		ExtractFileOpsFromMessage(message, &fileOps)
	}
	return &Preparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		KeptMessages:        keptMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        EstimateContextTokens(contextMessages).Tokens,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}, nil
}

func messagesFromEntries(entries []session.Entry, start int, end int) []agent.AgentMessage {
	if start < 0 {
		start = 0
	}
	if end > len(entries) {
		end = len(entries)
	}
	if end < start {
		end = start
	}
	messages := make([]agent.AgentMessage, 0, end-start)
	for _, entry := range entries[start:end] {
		if message, ok := messageFromEntry(entry); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func mergeCompactionDetails(fileOps *FileOperations, details any) {
	if fileOps == nil || details == nil {
		return
	}
	ensureFileOps(fileOps)
	values, ok := details.(map[string]any)
	if !ok {
		return
	}
	for _, file := range stringSliceFromAny(values["readFiles"]) {
		fileOps.Read[file] = struct{}{}
	}
	for _, file := range stringSliceFromAny(values["modifiedFiles"]) {
		fileOps.Edited[file] = struct{}{}
	}
}

func stringSliceFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// Compact generates compaction summary data from prepared session history.
// Mirrors TS src/harness/compaction/compaction.ts:627-706 (compact).
func Compact(ctx context.Context, prep *Preparation, model ai.Model, apiKey string, headers map[string]string, customInstructions string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel) (Result, error) {
	if prep == nil {
		return Result{}, &CompactionError{Code: "invalid_preparation", Msg: "compaction preparation is nil"}
	}
	if prep.FirstKeptEntryID == "" {
		return Result{}, &CompactionError{Code: "invalid_session", Msg: "First kept entry has no UUID - session may need migration"}
	}

	settings := withDefaults(prep.Settings)
	var summary string

	if prep.IsSplitTurn && len(prep.TurnPrefixMessages) > 0 {
		// Summarize the history and the turn prefix separately, then join.
		historySummary := "No prior history."
		if len(prep.MessagesToSummarize) > 0 {
			generated, err := GenerateSummary(ctx, prep.MessagesToSummarize, model, settings.ReserveTokens, apiKey, headers, customInstructions, prep.PreviousSummary, registry, thinkingLevel)
			if err != nil {
				return Result{}, err
			}
			historySummary = generated
		}
		turnPrefixSummary, err := generateTurnPrefixSummary(ctx, prep.TurnPrefixMessages, model, settings.ReserveTokens, apiKey, headers, registry, thinkingLevel)
		if err != nil {
			return Result{}, err
		}
		summary = historySummary + "\n\n---\n\n**Turn Context (split turn):**\n\n" + turnPrefixSummary
	} else {
		generated, err := GenerateSummary(ctx, prep.MessagesToSummarize, model, settings.ReserveTokens, apiKey, headers, customInstructions, prep.PreviousSummary, registry, thinkingLevel)
		if err != nil {
			return Result{}, err
		}
		summary = generated
	}

	if limit := settings.SummaryMaxChars; limit > 0 && len(summary) > limit {
		summary = summary[:limit] + "\n[summary truncated]"
	}
	readFiles, modifiedFiles := ComputeFileLists(prep.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)
	resultMessages := append([]agent.AgentMessage{createCompactionSummaryMessage(summary, prep.TokensBefore, "")}, prep.KeptMessages...)
	return Result{
		Summary:          summary,
		FirstKeptEntryID: prep.FirstKeptEntryID,
		TokensBefore:     prep.TokensBefore,
		Details: map[string]any{
			"readFiles":     readFiles,
			"modifiedFiles": modifiedFiles,
		},
		KeptMessages:   resultMessages,
		TokensAfter:    EstimateContextTokens(resultMessages).Tokens,
		MessagesBefore: len(prep.MessagesToSummarize) + len(prep.KeptMessages),
	}, nil
}

// summaryMaxTokens mirrors TS:
//
//	Math.min(Math.floor(fraction * reserveTokens), model.maxTokens > 0 ? model.maxTokens : Infinity)
//
// model.MaxOutput maps to the TS Model.maxTokens field (see ai.Model marshalling).
func summaryMaxTokens(fraction float64, reserveTokens int, model ai.Model) int {
	budget := int(math.Floor(fraction * float64(reserveTokens)))
	if model.MaxOutput > 0 && model.MaxOutput < budget {
		return model.MaxOutput
	}
	return budget
}

// GenerateSummary generates or updates a conversation summary for compaction.
// Mirrors TS src/harness/compaction/compaction.ts:456-519 (generateSummary).
func GenerateSummary(ctx context.Context, messages []agent.AgentMessage, model ai.Model, reserveTokens int, apiKey string, headers map[string]string, customInstructions string, previousSummary string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel) (string, error) {
	maxTokens := summaryMaxTokens(0.8, reserveTokens, model)

	basePrompt := summarizationPrompt
	if previousSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	if custom := strings.TrimSpace(customInstructions); custom != "" {
		basePrompt = basePrompt + "\n\nAdditional focus: " + custom
	}

	llmMessages, err := messagesToLLM(messages)
	if err != nil {
		return "", err
	}
	conversationText := SerializeConversation(llmMessages)
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n"
	if previousSummary != "" {
		promptText += "<previous-summary>\n" + previousSummary + "\n</previous-summary>\n\n"
	}
	promptText += basePrompt

	return runSummarization(ctx, model, maxTokens, promptText, apiKey, headers, registry, thinkingLevel, "Summarization aborted", "Summarization failed: ")
}

// generateTurnPrefixSummary summarizes the prefix of a split turn.
// Mirrors TS src/harness/compaction/compaction.ts:707-756 (generateTurnPrefixSummary).
func generateTurnPrefixSummary(ctx context.Context, messages []agent.AgentMessage, model ai.Model, reserveTokens int, apiKey string, headers map[string]string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel) (string, error) {
	maxTokens := summaryMaxTokens(0.5, reserveTokens, model)
	llmMessages, err := messagesToLLM(messages)
	if err != nil {
		return "", err
	}
	conversationText := SerializeConversation(llmMessages)
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + turnPrefixSummarizationPrompt
	return runSummarization(ctx, model, maxTokens, promptText, apiKey, headers, registry, thinkingLevel, "Turn prefix summarization aborted", "Turn prefix summarization failed: ")
}

// runSummarization issues a summarization completion and maps stop reasons to errors.
func runSummarization(ctx context.Context, model ai.Model, maxTokens int, promptText string, apiKey string, headers map[string]string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel, abortedMsg string, failedPrefix string) (string, error) {
	options := ai.SimpleStreamOptions{APIKey: apiKey, Headers: headers, MaxTokens: maxTokens}
	if model.Reasoning && thinkingLevel != "" && thinkingLevel != ai.ThinkingOff {
		options.Reasoning = thinkingLevel
	}
	complete := ai.CompleteSimple
	if registry != nil {
		complete = registry.CompleteSimple
	}
	message, err := complete(ctx, model, ai.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages:     []ai.Message{ai.NewUserMessage(promptText, nil)},
	}, options)
	if err != nil {
		return "", &CompactionError{Code: "summarization_failed", Msg: "summarization failed", Err: err}
	}
	if message.StopReason == "aborted" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = abortedMsg
		}
		return "", &CompactionError{Code: "aborted", Msg: msg}
	}
	if message.StopReason == "error" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = "Unknown error"
		}
		return "", &CompactionError{Code: "summarization_failed", Msg: failedPrefix + msg}
	}
	return summaryTextContent(message), nil
}

// summaryTextContent joins the text blocks of a completion with newlines,
// mirroring TS `response.content.filter(text).map(text).join("\n")`.
func summaryTextContent(message ai.AssistantMessage) string {
	var texts []string
	for _, block := range message.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// summarizationSystemPrompt mirrors TS SUMMARIZATION_SYSTEM_PROMPT (compaction.ts:379-381).
const summarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

// summarizationPrompt mirrors TS SUMMARIZATION_PROMPT (compaction.ts:383-414).
const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// updateSummarizationPrompt mirrors TS UPDATE_SUMMARIZATION_PROMPT (compaction.ts:416-453).
const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// turnPrefixSummarizationPrompt mirrors TS TURN_PREFIX_SUMMARIZATION_PROMPT (compaction.ts:609-622).
const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`
