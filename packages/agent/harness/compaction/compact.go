package compaction

import (
	"context"
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

func Compact(ctx context.Context, prep *Preparation, model ai.Model, apiKey string, headers map[string]string, customInstructions string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel) (Result, error) {
	if prep == nil {
		return Result{}, &CompactionError{Code: "invalid_preparation", Msg: "compaction preparation is nil"}
	}
	llmMessages, err := messagesToLLM(prep.MessagesToSummarize)
	if err != nil {
		return Result{}, err
	}
	conversationText := SerializeConversation(llmMessages)
	summary, err := GenerateSummary(ctx, conversationText, customInstructions, model, apiKey, headers, registry, thinkingLevel)
	if err != nil {
		return Result{}, err
	}
	if prep.PreviousSummary != "" {
		summary = strings.TrimSpace(prep.PreviousSummary) + "\n\n" + strings.TrimSpace(summary)
	}
	if limit := prep.Settings.SummaryMaxChars; limit > 0 && len(summary) > limit {
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

func GenerateSummary(ctx context.Context, conversationText string, customInstructions string, model ai.Model, apiKey string, headers map[string]string, registry *ai.ModelRegistry, thinkingLevel ai.ThinkingLevel) (string, error) {
	instructions := compactionPrompt
	if custom := strings.TrimSpace(customInstructions); custom != "" {
		instructions += "\n\nAdditional instructions:\n" + custom
	}
	prompt := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + instructions
	options := ai.SimpleStreamOptions{APIKey: apiKey, Headers: headers, MaxTokens: 2048}
	if model.Reasoning && thinkingLevel != "" && thinkingLevel != ai.ThinkingOff {
		options.Reasoning = thinkingLevel
	}
	complete := ai.CompleteSimple
	if registry != nil {
		complete = registry.CompleteSimple
	}
	message, err := complete(ctx, model, ai.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages:     []ai.Message{ai.NewUserMessage(prompt, nil)},
	}, options)
	if err != nil {
		return "", &CompactionError{Code: "summarization_failed", Msg: "summarization failed", Err: err}
	}
	if message.StopReason == "aborted" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = "Summarization aborted"
		}
		return "", &CompactionError{Code: "aborted", Msg: msg}
	}
	if message.StopReason == "error" {
		msg := message.ErrorMessage
		if msg == "" {
			msg = "Unknown error"
		}
		return "", &CompactionError{Code: "summarization_failed", Msg: "Summarization failed: " + msg}
	}
	return strings.TrimSpace(ai.MessageText(message)), nil
}

const summarizationSystemPrompt = "You are summarizing previous conversation context for a coding agent."

const compactionPrompt = `Summarize the conversation above for future context.

Preserve concrete file paths, commands, errors, decisions, constraints, and unfinished work. Keep it concise.`
