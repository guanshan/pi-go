package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

const (
	defaultCompactionReserveTokens    = 16384
	defaultCompactionKeepRecentTokens = 20000
	estimatedImageChars               = 4800
	toolResultSummaryMaxChars         = 2000
)

const summarizationSystemPrompt = "You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified.\n\nDo NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary."

const summarizationPrompt = "The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.\n\nUse this EXACT format:\n\n## Goal\n[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]\n\n## Constraints & Preferences\n- [Any constraints, preferences, or requirements mentioned by user]\n- [Or \"(none)\" if none were mentioned]\n\n## Progress\n### Done\n- [x] [Completed tasks/changes]\n\n### In Progress\n- [ ] [Current work]\n\n### Blocked\n- [Issues preventing progress, if any]\n\n## Key Decisions\n- **[Decision]**: [Brief rationale]\n\n## Next Steps\n1. [Ordered list of what should happen next]\n\n## Critical Context\n- [Any data, examples, or references needed to continue]\n- [Or \"(none)\" if not applicable]\n\nKeep each section concise. Preserve exact file paths, function names, and error messages."

const updateSummarizationPrompt = "The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.\n\nUpdate the existing structured summary with new information. RULES:\n- PRESERVE all existing information from the previous summary\n- ADD new progress, decisions, and context from the new messages\n- UPDATE the Progress section: move items from \"In Progress\" to \"Done\" when completed\n- UPDATE \"Next Steps\" based on what was accomplished\n- PRESERVE exact file paths, function names, and error messages\n- If something is no longer relevant, you may remove it\n\nUse this EXACT format:\n\n## Goal\n[Preserve existing goals, add new ones if the task expanded]\n\n## Constraints & Preferences\n- [Preserve existing, add new ones discovered]\n\n## Progress\n### Done\n- [x] [Include previously done items AND newly completed items]\n\n### In Progress\n- [ ] [Current work - update based on progress]\n\n### Blocked\n- [Current blockers - remove if resolved]\n\n## Key Decisions\n- **[Decision]**: [Brief rationale] (preserve all previous, add new)\n\n## Next Steps\n1. [Update based on current state]\n\n## Critical Context\n- [Preserve important context, add new if needed]\n\nKeep each section concise. Preserve exact file paths, function names, and error messages."

const turnPrefixSummarizationPrompt = "This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.\n\nSummarize the prefix to provide context for the retained suffix:\n\n## Original Request\n[What did the user ask for in this turn?]\n\n## Early Progress\n- [Key decisions and work done in the prefix]\n\n## Context for Suffix\n- [Information needed to understand the retained recent work]\n\nBe concise. Focus on what's needed to understand the kept suffix."

type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles,omitempty"`
	ModifiedFiles []string `json:"modifiedFiles,omitempty"`
}

type compactionPreparation struct {
	FirstKeptEntryID    string
	MessagesToSummarize []ai.Message
	TurnPrefixMessages  []ai.Message
	IsSplitTurn         bool
	TokensBefore        int
	PreviousSummary     string
	FileOps             fileOperations
	Settings            CompactionSettings
}

type compactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          CompactionDetails
}

type fileOperations struct {
	Read    map[string]struct{}
	Written map[string]struct{}
	Edited  map[string]struct{}
}

type cutPointResult struct {
	FirstKeptEntryIndex int
	TurnStartIndex      int
	IsSplitTurn         bool
}

func compactionSettingsFromManager(settings *SettingsManager) CompactionSettings {
	out := CompactionSettings{
		Enabled:          true,
		ReserveTokens:    defaultCompactionReserveTokens,
		KeepRecentTokens: defaultCompactionKeepRecentTokens,
	}
	if settings == nil {
		return out
	}
	out.Enabled = settings.AutoCompactionEnabled()
	if reserve := settings.CompactionReserveTokens(); reserve > 0 {
		out.ReserveTokens = reserve
	}
	if keep := settings.CompactionKeepRecentTokens(); keep > 0 {
		out.KeepRecentTokens = keep
	}
	return out
}

func shouldCompact(contextTokens, contextWindow int, settings CompactionSettings) bool {
	if !settings.Enabled || contextWindow <= 0 {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

func prepareCompaction(pathEntries []SessionEntry, settings CompactionSettings) *compactionPreparation {
	if len(pathEntries) == 0 {
		return nil
	}
	if pathEntries[len(pathEntries)-1].Type == "compaction" {
		return nil
	}

	prevCompactionIndex := -1
	for i := len(pathEntries) - 1; i >= 0; i-- {
		if pathEntries[i].Type == "compaction" {
			prevCompactionIndex = i
			break
		}
	}

	boundaryStart := 0
	previousSummary := ""
	if prevCompactionIndex >= 0 {
		prevCompaction := pathEntries[prevCompactionIndex]
		previousSummary = prevCompaction.Summary
		firstKeptEntryIndex := -1
		for i, entry := range pathEntries {
			if entry.ID == prevCompaction.FirstKeptID {
				firstKeptEntryIndex = i
				break
			}
		}
		if firstKeptEntryIndex >= 0 {
			boundaryStart = firstKeptEntryIndex
		} else {
			boundaryStart = prevCompactionIndex + 1
		}
	}

	tokensBefore := estimateContextTokens(pathEntries)
	cutPoint := findCutPoint(pathEntries, boundaryStart, len(pathEntries), settings.KeepRecentTokens)
	if cutPoint.FirstKeptEntryIndex < 0 || cutPoint.FirstKeptEntryIndex >= len(pathEntries) {
		return nil
	}
	firstKeptEntryID := pathEntries[cutPoint.FirstKeptEntryIndex].ID
	if firstKeptEntryID == "" {
		return nil
	}

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}
	if historyEnd < boundaryStart {
		historyEnd = boundaryStart
	}

	messagesToSummarize := make([]ai.Message, 0, max(0, historyEnd-boundaryStart))
	for i := boundaryStart; i < historyEnd; i++ {
		if msg, ok := compactionMessageFromEntry(pathEntries[i], false); ok {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}

	turnPrefixMessages := []ai.Message{}
	if cutPoint.IsSplitTurn {
		for i := cutPoint.TurnStartIndex; i < cutPoint.FirstKeptEntryIndex; i++ {
			if msg, ok := compactionMessageFromEntry(pathEntries[i], false); ok {
				turnPrefixMessages = append(turnPrefixMessages, msg)
			}
		}
	}

	fileOps := extractCompactionFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)
	if cutPoint.IsSplitTurn {
		for _, msg := range turnPrefixMessages {
			extractFileOpsFromMessage(msg, fileOps)
		}
	}

	return &compactionPreparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}
}

func runCompaction(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, preparation *compactionPreparation, customInstructions string) (*compactionResult, error) {
	if preparation == nil {
		return nil, nil
	}

	summary := ""
	if preparation.IsSplitTurn && len(preparation.TurnPrefixMessages) > 0 {
		historySummary := "No prior history."
		if len(preparation.MessagesToSummarize) > 0 {
			generated, err := generateCompactionSummary(ctx, registry, model, thinkingLevel, preparation.MessagesToSummarize, preparation.Settings.ReserveTokens, customInstructions, preparation.PreviousSummary)
			if err != nil {
				return nil, err
			}
			historySummary = generated
		}
		turnSummary, err := generateTurnPrefixSummary(ctx, registry, model, thinkingLevel, preparation.TurnPrefixMessages, preparation.Settings.ReserveTokens)
		if err != nil {
			return nil, err
		}
		summary = historySummary + "\n\n---\n\n**Turn Context (split turn):**\n\n" + turnSummary
	} else {
		generated, err := generateCompactionSummary(ctx, registry, model, thinkingLevel, preparation.MessagesToSummarize, preparation.Settings.ReserveTokens, customInstructions, preparation.PreviousSummary)
		if err != nil {
			return nil, err
		}
		summary = generated
	}

	details := computeCompactionDetails(preparation.FileOps)
	summary += formatCompactionFileOperations(details)

	return &compactionResult{
		Summary:          summary,
		FirstKeptEntryID: preparation.FirstKeptEntryID,
		TokensBefore:     preparation.TokensBefore,
		Details:          details,
	}, nil
}

func generateCompactionSummary(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, messages []ai.Message, reserveTokens int, customInstructions, previousSummary string) (string, error) {
	basePrompt := summarizationPrompt
	if previousSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	if customInstructions != "" {
		basePrompt += "\n\nAdditional focus: " + customInstructions
	}
	// Convert to LLM messages first (handles custom types like bashExecution,
	// custom, branchSummary, compactionSummary) so serializeConversation sees
	// only role:user/assistant/toolResult messages, matching TS.
	llmMessages, _ := convertSessionMessagesToLLM(messages)
	conversationText := serializeConversation(llmMessages)
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n"
	if previousSummary != "" {
		promptText += "<previous-summary>\n" + previousSummary + "\n</previous-summary>\n\n"
	}
	promptText += basePrompt
	return completeCompactionPrompt(ctx, registry, model, thinkingLevel, reserveTokens, promptText)
}

func generateTurnPrefixSummary(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, messages []ai.Message, reserveTokens int) (string, error) {
	llmMessages, _ := convertSessionMessagesToLLM(messages)
	conversationText := serializeConversation(llmMessages)
	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + turnPrefixSummarizationPrompt
	maxTokens := reserveTokens / 2
	if maxTokens <= 0 {
		maxTokens = reserveTokens
	}
	return completeCompactionPromptWithMaxTokens(ctx, registry, model, thinkingLevel, maxTokens, promptText)
}

func completeCompactionPrompt(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, reserveTokens int, promptText string) (string, error) {
	maxTokens := int(math.Floor(0.8 * float64(reserveTokens)))
	if maxTokens <= 0 {
		maxTokens = reserveTokens
	}
	return completeCompactionPromptWithMaxTokens(ctx, registry, model, thinkingLevel, maxTokens, promptText)
}

func completeCompactionPromptWithMaxTokens(ctx context.Context, registry *ai.ModelRegistry, model ai.Model, thinkingLevel ai.ThinkingLevel, maxTokens int, promptText string) (string, error) {
	if model.MaxOutput > 0 && (maxTokens <= 0 || maxTokens > model.MaxOutput) {
		maxTokens = model.MaxOutput
	}
	options := ai.SimpleStreamOptions{MaxTokens: maxTokens}
	if model.Reasoning && thinkingLevel != "" && thinkingLevel != ai.ThinkingOff {
		options.Reasoning = thinkingLevel
	}
	llmContext := ai.Context{
		SystemPrompt: summarizationSystemPrompt,
		Messages:     []ai.Message{ai.NewUserMessage(promptText, nil)},
	}
	var (
		response ai.AssistantMessage
		err      error
	)
	if registry != nil {
		response, err = registry.CompleteSimple(ctx, model, llmContext, options)
	} else {
		response, err = ai.CompleteSimple(ctx, model, llmContext, options)
	}
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	if response.StopReason == "error" {
		return "", fmt.Errorf("summarization failed: %s", firstNonEmpty(response.ErrorMessage, "unknown error"))
	}
	return strings.TrimSpace(ai.MessageText(response)), nil
}

func estimateContextTokens(entries []SessionEntry) int {
	messages := buildContextMessages(entries)
	lastUsageIndex := -1
	usageTokens := 0
	for i := len(messages) - 1; i >= 0; i-- {
		assistant, ok := ai.AsAssistantMessage(messages[i])
		if !ok || assistant.StopReason == "aborted" || assistant.StopReason == "error" {
			continue
		}
		usageTokens = calculateContextTokens(assistant.Usage)
		lastUsageIndex = i
		break
	}
	if lastUsageIndex < 0 {
		tokens := 0
		for _, message := range messages {
			tokens += estimateCompactionMessageTokens(message)
		}
		return tokens
	}
	tokens := usageTokens
	for i := lastUsageIndex + 1; i < len(messages); i++ {
		tokens += estimateCompactionMessageTokens(messages[i])
	}
	return tokens
}

func calculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func buildContextMessages(entries []SessionEntry) []ai.Message {
	ctx := (&SessionManager{Entries: entries, CurrentID: currentIDFromEntries(entries)}).BuildContext()
	return ctx.Messages
}

func currentIDFromEntries(entries []SessionEntry) *string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].ID != "" && treeEntry(entries[i].Type) {
			id := entries[i].ID
			return &id
		}
	}
	return nil
}

func extractCompactionFileOperations(messages []ai.Message, entries []SessionEntry, prevCompactionIndex int) fileOperations {
	ops := newFileOperations()
	if prevCompactionIndex >= 0 {
		details := compactionDetailsFromAny(entries[prevCompactionIndex].Details)
		for _, path := range details.ReadFiles {
			ops.Read[path] = struct{}{}
		}
		for _, path := range details.ModifiedFiles {
			ops.Edited[path] = struct{}{}
		}
	}
	for _, msg := range messages {
		extractFileOpsFromMessage(msg, ops)
	}
	return ops
}

func newFileOperations() fileOperations {
	return fileOperations{
		Read:    map[string]struct{}{},
		Written: map[string]struct{}{},
		Edited:  map[string]struct{}{},
	}
}

func extractFileOpsFromMessage(message ai.Message, ops fileOperations) {
	assistant, ok := ai.AsAssistantMessage(message)
	if !ok {
		return
	}
	for _, block := range assistant.Content {
		if block.Type != "toolCall" || len(block.Arguments) == 0 {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(block.Arguments, &args); err != nil {
			continue
		}
		path, _ := args["path"].(string)
		if strings.TrimSpace(path) == "" {
			continue
		}
		switch block.Name {
		case "read":
			ops.Read[path] = struct{}{}
		case "write":
			ops.Written[path] = struct{}{}
		case "edit":
			ops.Edited[path] = struct{}{}
		}
	}
}

func computeCompactionDetails(ops fileOperations) CompactionDetails {
	modifiedSet := map[string]struct{}{}
	for path := range ops.Written {
		modifiedSet[path] = struct{}{}
	}
	for path := range ops.Edited {
		modifiedSet[path] = struct{}{}
	}
	readFiles := make([]string, 0, len(ops.Read))
	for path := range ops.Read {
		if _, modified := modifiedSet[path]; modified {
			continue
		}
		readFiles = append(readFiles, path)
	}
	modifiedFiles := make([]string, 0, len(modifiedSet))
	for path := range modifiedSet {
		modifiedFiles = append(modifiedFiles, path)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return CompactionDetails{ReadFiles: readFiles, ModifiedFiles: modifiedFiles}
}

func formatCompactionFileOperations(details CompactionDetails) string {
	sections := make([]string, 0, 2)
	if len(details.ReadFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(details.ReadFiles, "\n")+"\n</read-files>")
	}
	if len(details.ModifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(details.ModifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func compactionDetailsFromAny(value any) CompactionDetails {
	if value == nil {
		return CompactionDetails{}
	}
	switch details := value.(type) {
	case CompactionDetails:
		return details
	case *CompactionDetails:
		if details != nil {
			return *details
		}
		return CompactionDetails{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return CompactionDetails{}
	}
	var details CompactionDetails
	if err := json.Unmarshal(raw, &details); err != nil {
		return CompactionDetails{}
	}
	return details
}

func compactionMessageFromEntry(entry SessionEntry, includeCompaction bool) (ai.Message, bool) {
	switch entry.Type {
	case "message":
		if entry.Message == nil {
			return nil, false
		}
		if !includeCompaction && ai.MessageRole(entry.Message) == "compactionSummary" {
			return nil, false
		}
		return entry.Message, true
	case "custom_message":
		return CustomSessionMessage{Role: "custom", CustomType: entry.CustomType, Content: entry.Content, Display: entry.Display, Details: entry.Details, TimestampMs: sessionEntryTimestamp(entry.Timestamp)}, true
	case "branch_summary":
		return BranchSummaryMessage{Role: "branchSummary", Summary: entry.Summary, FromID: entry.FromID, TimestampMs: sessionEntryTimestamp(entry.Timestamp)}, true
	case "compaction":
		if !includeCompaction {
			return nil, false
		}
		return CompactionSummaryMessage{Role: "compactionSummary", Summary: entry.Summary, TokensBefore: entry.TokensBefore, TimestampMs: sessionEntryTimestamp(entry.Timestamp)}, true
	default:
		return nil, false
	}
}

func sessionEntryTimestamp(value string) int64 {
	if value == "" {
		return time.Now().UnixMilli()
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return parsed.UnixMilli()
}

func estimateCompactionMessageTokens(message ai.Message) int {
	switch ai.MessageRole(message) {
	case "user":
		return int(math.Ceil(float64(estimateTextAndImageChars(ai.MessageBlocks(message))) / 4.0))
	case "assistant":
		chars := 0
		assistant, _ := ai.AsAssistantMessage(message)
		for _, block := range assistant.Content {
			switch block.Type {
			case "text":
				chars += len(block.Text)
			case "thinking":
				chars += len(block.Thinking)
			case "toolCall":
				chars += len(block.Name) + len(block.Arguments)
			}
		}
		return int(math.Ceil(float64(chars) / 4.0))
	case "toolResult", "custom":
		return int(math.Ceil(float64(estimateTextAndImageChars(ai.MessageBlocks(message))) / 4.0))
	case "branchSummary", "compactionSummary":
		if summary, ok := sessionSummaryText(message); ok {
			return int(math.Ceil(float64(len(summary)) / 4.0))
		}
	}
	return 0
}

func estimateTextAndImageChars(blocks []ai.ContentBlock) int {
	chars := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			chars += len(block.Text)
		case "image":
			chars += estimatedImageChars
		}
	}
	return chars
}

func findValidCutPoints(entries []SessionEntry, startIndex, endIndex int) []int {
	cutPoints := []int{}
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		switch entry.Type {
		case "message":
			switch ai.MessageRole(entry.Message) {
			case "user", "assistant", "custom", "branchSummary", "compactionSummary", "bashExecution":
				cutPoints = append(cutPoints, i)
			}
		case "branch_summary", "custom_message":
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

func findTurnStartIndex(entries []SessionEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		entry := entries[i]
		if entry.Type == "branch_summary" || entry.Type == "custom_message" {
			return i
		}
		if entry.Type != "message" {
			continue
		}
		role := ai.MessageRole(entry.Message)
		if role == "user" || role == "bashExecution" {
			return i
		}
	}
	return -1
}

func findCutPoint(entries []SessionEntry, startIndex, endIndex, keepRecentTokens int) cutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return cutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}
	accumulatedTokens := 0
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		accumulatedTokens += estimateCompactionMessageTokens(entry.Message)
		if accumulatedTokens < keepRecentTokens {
			continue
		}
		for _, point := range cutPoints {
			if point >= i {
				cutIndex = point
				break
			}
		}
		break
	}
	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if prevEntry.Type == "compaction" || prevEntry.Type == "message" {
			break
		}
		cutIndex--
	}
	cutEntry := entries[cutIndex]
	isUserMessage := cutEntry.Type == "message" && ai.MessageRole(cutEntry.Message) == "user"
	turnStartIndex := -1
	if !isUserMessage {
		turnStartIndex = findTurnStartIndex(entries, cutIndex, startIndex)
	}
	return cutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !isUserMessage && turnStartIndex != -1,
	}
}

// serializeConversation mirrors TS compaction/utils.ts serializeConversation.
// Callers MUST run messages through convertSessionMessagesToLLM first, so only
// user/assistant/toolResult roles reach this function (bashExecution/custom/
// branchSummary/compactionSummary are converted to role:user beforehand).
func serializeConversation(messages []ai.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		switch ai.MessageRole(msg) {
		case "user":
			if content := ai.MessageText(msg); content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case "assistant":
			textParts := make([]string, 0)
			thinkingParts := make([]string, 0)
			toolCalls := make([]string, 0)
			for _, block := range ai.MessageBlocks(msg) {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "thinking":
					thinkingParts = append(thinkingParts, block.Thinking)
				case "toolCall":
					toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", block.Name, serializeToolCallArguments(block.Arguments)))
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case "toolResult":
			if content := ai.MessageText(msg); content != "" {
				parts = append(parts, "[Tool result]: "+truncateCompactionText(content, toolResultSummaryMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// serializeToolCallArguments renders toolCall arguments as TS does:
// `Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ")`.
// Object key order matches the raw JSON byte order (JS object enumeration order).
func serializeToolCallArguments(arguments json.RawMessage) string {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return ""
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return ""
	}
	pairs := make([]string, 0)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return strings.Join(pairs, ", ")
		}
		key, ok := keyTok.(string)
		if !ok {
			return strings.Join(pairs, ", ")
		}
		var value any
		if err := dec.Decode(&value); err != nil {
			return strings.Join(pairs, ", ")
		}
		encoded, err := marshalJSONNoHTMLEscape(value)
		if err != nil {
			return strings.Join(pairs, ", ")
		}
		pairs = append(pairs, key+"="+string(encoded))
	}
	return strings.Join(pairs, ", ")
}

func sessionSummaryText(message ai.Message) (string, bool) {
	switch m := message.(type) {
	case BranchSummaryMessage:
		return m.Summary, true
	case *BranchSummaryMessage:
		if m == nil {
			return "", false
		}
		return m.Summary, true
	case CompactionSummaryMessage:
		return m.Summary, true
	case *CompactionSummaryMessage:
		if m == nil {
			return "", false
		}
		return m.Summary, true
	default:
		custom, ok := ai.AsCustomMessage(message)
		if !ok {
			return "", false
		}
		return custom.Summary, true
	}
}

func truncateCompactionText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	truncatedChars := len(text) - maxChars
	return text[:maxChars] + fmt.Sprintf("\n\n[... %d more characters truncated]", truncatedChars)
}
