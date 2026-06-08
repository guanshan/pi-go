package compaction

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/agent/harness/session"
	"github.com/guanshan/pi-go/packages/ai"
)

type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex *int
}

func ShouldCompact(contextTokens int, contextWindow int, settings Settings) bool {
	settings = withDefaults(settings)
	return settings.Enabled && contextTokens > contextWindow-settings.ReserveTokens
}

func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage, ok := assistantUsage(messages[i]); ok {
			usageTokens := CalculateContextTokens(usage)
			trailingTokens := 0
			for j := i + 1; j < len(messages); j++ {
				trailingTokens += EstimateTokens(messages[j])
			}
			idx := i
			return ContextUsageEstimate{
				Tokens:         usageTokens + trailingTokens,
				UsageTokens:    usageTokens,
				TrailingTokens: trailingTokens,
				LastUsageIndex: &idx,
			}
		}
	}
	estimated := 0
	for _, message := range messages {
		estimated += EstimateTokens(message)
	}
	return ContextUsageEstimate{Tokens: estimated, TrailingTokens: estimated}
}

func CalculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func GetLastAssistantUsage(entries []session.Entry) (ai.Usage, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entry, ok := entries[i].(session.MessageEntry); ok {
			if usage, ok := assistantUsage(entry.Message); ok {
				return usage, true
			}
		}
	}
	return ai.Usage{}, false
}

func assistantUsage(message agent.AgentMessage) (ai.Usage, bool) {
	assistant, ok := ai.AsAssistantMessage(message)
	if !ok || assistant.Usage.IsZero() || assistant.StopReason == "aborted" || assistant.StopReason == "error" {
		return ai.Usage{}, false
	}
	return assistant.Usage, true
}

type CutPointResult struct {
	FirstKeptEntryIndex int
	TurnStartIndex      int
	IsSplitTurn         bool
}

func FindCutPoint(entries []session.Entry, startIndex int, endIndex int, keepTokens int) CutPointResult {
	if startIndex < 0 {
		startIndex = 0
	}
	if endIndex > len(entries) {
		endIndex = len(entries)
	}
	if endIndex < startIndex {
		endIndex = startIndex
	}
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}
	total := 0
	cutIndex := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		message, ok := messageEntryMessage(entries[i])
		if !ok {
			continue
		}
		total += EstimateTokens(message)
		if total >= keepTokens {
			for _, point := range cutPoints {
				if point >= i {
					cutIndex = point
					break
				}
			}
			break
		}
	}
	for cutIndex > startIndex {
		prev := entries[cutIndex-1]
		if _, ok := prev.(session.CompactionEntry); ok {
			break
		}
		if _, ok := prev.(session.MessageEntry); ok {
			break
		}
		cutIndex--
	}
	cutEntry := entries[cutIndex]
	isUser := false
	if message, ok := messageEntryMessage(cutEntry); ok && ai.MessageRole(message) == "user" {
		isUser = true
	}
	turnStartIndex := -1
	if !isUser {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !isUser && turnStartIndex != -1,
	}
}

func findValidCutPoints(entries []session.Entry, startIndex int, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if message, ok := messageEntryMessage(entry); ok {
			switch ai.MessageRole(message) {
			case "bashExecution", "custom", "branchSummary", "compactionSummary", "user", "assistant":
				cutPoints = append(cutPoints, i)
			case "toolResult":
			}
		}
		switch entry.(type) {
		case session.BranchSummaryEntry, session.CustomMessageEntry:
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

func FindTurnStartIndex(entries []session.Entry, entryIndex int, startIndex int) int {
	if entryIndex >= len(entries) {
		entryIndex = len(entries) - 1
	}
	if startIndex < 0 {
		startIndex = 0
	}
	for i := entryIndex; i >= startIndex; i-- {
		switch entries[i].(type) {
		case session.BranchSummaryEntry, session.CustomMessageEntry:
			return i
		}
		message, ok := messageEntryMessage(entries[i])
		if !ok {
			continue
		}
		switch ai.MessageRole(message) {
		case "user", "bashExecution":
			return i
		}
	}
	return -1
}

func messageEntryMessage(entry session.Entry) (agent.AgentMessage, bool) {
	messageEntry, ok := entry.(session.MessageEntry)
	if !ok || messageEntry.Message == nil {
		return nil, false
	}
	return messageEntry.Message, true
}

// utf16Len returns the number of UTF-16 code units in s, matching the
// JavaScript String.length semantics that TS estimateTokens relies on: each
// rune <= U+FFFF counts as one code unit and each rune above U+FFFF (encoded as
// a surrogate pair in UTF-16) counts as two. Go's len() counts UTF-8 bytes,
// which over-counts non-ASCII text, so the token heuristic must use this helper
// to stay byte-for-byte identical with the TS port.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func EstimateTokens(message agent.AgentMessage) int {
	chars := 0
	switch ai.MessageRole(message) {
	case "user", "toolResult", "custom":
		chars = estimateContentChars(ai.MessageBlocks(message))
	case "assistant":
		for _, block := range ai.MessageBlocks(message) {
			switch block.Type {
			case "text":
				chars += utf16Len(block.Text)
			case "thinking":
				chars += utf16Len(block.Thinking)
			case "toolCall":
				chars += utf16Len(block.Name) + utf16Len(safeJSON(block.Arguments))
			}
		}
	case "bashExecution":
		if custom, ok := ai.AsCustomMessage(message); ok {
			chars = utf16Len(custom.Command) + utf16Len(custom.Output)
		}
	case "branchSummary", "compactionSummary":
		if custom, ok := ai.AsCustomMessage(message); ok {
			chars = utf16Len(custom.Summary)
		} else {
			chars = utf16Len(ai.MessageText(message))
		}
	default:
		return 0
	}
	return int(math.Ceil(float64(chars) / 4.0))
}

func estimateMessageTokens(message agent.AgentMessage) int {
	return EstimateTokens(message)
}

func estimateContentChars(blocks []ai.ContentBlock) int {
	chars := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			chars += utf16Len(block.Text)
		case "image":
			chars += 4800
		}
	}
	return chars
}

func safeJSON(value any) string {
	// Mirror TS safeJsonStringify, which wraps JSON.stringify (compaction.ts:28-34).
	// JSON.stringify does NOT HTML-escape <, >, or &, so the token-estimation
	// character count must match: use an encoder with SetEscapeHTML(false)
	// instead of json.Marshal (which escapes those to < etc.). The encoder
	// appends a trailing newline that JSON.stringify omits, so trim it.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return "[unserializable]"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
