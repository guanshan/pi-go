package session

import (
	"context"
	"time"

	"github.com/guanshan/pi-go/packages/agent"
	"github.com/guanshan/pi-go/packages/ai"
)

type Context struct {
	Messages        []agent.AgentMessage
	ThinkingLevel   string
	Model           *ModelRef
	ActiveToolNames *[]string
}

type ModelRef struct {
	Provider string
	ModelID  string
}

func (s *Session) BuildContext(ctx context.Context) (Context, error) {
	leaf, err := s.storage.LeafID(ctx)
	if err != nil {
		return Context{}, err
	}
	entries, err := s.storage.PathToRoot(ctx, leaf)
	if err != nil {
		return Context{}, err
	}
	return BuildContextFromEntries(entries), nil
}

func BuildContextFromEntries(entries []Entry) Context {
	out := Context{}
	var summary *CompactionEntry
	compactionIdx := -1
	for i, entry := range entries {
		switch e := entry.(type) {
		case ThinkingLevelChangeEntry:
			out.ThinkingLevel = e.ThinkingLevel
		case ModelChangeEntry:
			out.Model = &ModelRef{Provider: e.Provider, ModelID: e.ModelID}
		case MessageEntry:
			if assistant, ok := ai.AsAssistantMessage(e.Message); ok {
				out.Model = &ModelRef{Provider: assistant.Provider, ModelID: assistant.Model}
			}
		case ActiveToolsChangeEntry:
			names := append([]string(nil), e.ActiveToolNames...)
			out.ActiveToolNames = &names
		}
		if compaction, ok := entry.(CompactionEntry); ok {
			copy := compaction
			summary = &copy
			compactionIdx = i
		}
	}
	if summary != nil {
		out.Messages = append(out.Messages, createCompactionSummaryMessage(summary.Summary, summary.TokensBefore, summary.EntryTimestamp()))
		foundFirstKept := false
		for i := 0; i < compactionIdx; i++ {
			entry := entries[i]
			if entry.EntryID() == summary.FirstKeptEntryID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendContextMessage(&out, entry)
			}
		}
		for i := compactionIdx + 1; i < len(entries); i++ {
			appendContextMessage(&out, entries[i])
		}
		return out
	}
	for _, entry := range entries {
		appendContextMessage(&out, entry)
	}
	return out
}

func appendContextMessage(out *Context, entry Entry) {
	switch e := entry.(type) {
	case MessageEntry:
		if e.Message != nil {
			out.Messages = append(out.Messages, e.Message)
		}
	case BranchSummaryEntry:
		if e.Summary != "" {
			out.Messages = append(out.Messages, createBranchSummaryMessage(e.Summary, e.FromID, e.EntryTimestamp()))
		}
	case CustomMessageEntry:
		out.Messages = append(out.Messages, createCustomMessage(e.CustomType, e.Content, e.Display, e.Details, e.EntryTimestamp()))
	}
}

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
