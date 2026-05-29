package session

import (
	"context"

	"github.com/guanshan/pi-go/packages/agent"
)

type Session struct {
	storage Storage
}

func New(s Storage) *Session {
	return &Session{storage: s}
}

func NewMemory(metadata Metadata, entries []Entry) (*Session, error) {
	storage, err := NewMemoryStorage(metadata, entries)
	if err != nil {
		return nil, err
	}
	return New(storage), nil
}

func (s *Session) Storage() Storage {
	return s.storage
}

func (s *Session) Metadata(ctx context.Context) (Metadata, error) {
	return s.storage.Metadata(ctx)
}

func (s *Session) LeafID(ctx context.Context) (*string, error) {
	return s.storage.LeafID(ctx)
}

func (s *Session) Entry(ctx context.Context, id string) (Entry, error) {
	return s.storage.Entry(ctx, id)
}

func (s *Session) Entries(ctx context.Context) ([]Entry, error) {
	return s.storage.Entries(ctx)
}

func (s *Session) Branch(ctx context.Context, fromID *string) ([]Entry, error) {
	if fromID == nil {
		leaf, err := s.storage.LeafID(ctx)
		if err != nil {
			return nil, err
		}
		fromID = leaf
	}
	return s.storage.PathToRoot(ctx, fromID)
}

func (s *Session) Label(ctx context.Context, id string) (string, bool) {
	return s.storage.Label(ctx, id)
}

func (s *Session) Name(ctx context.Context) (string, error) {
	branch, err := s.Branch(ctx, nil)
	if err != nil {
		return "", err
	}
	for i := len(branch) - 1; i >= 0; i-- {
		if info, ok := branch[i].(SessionInfoEntry); ok {
			return info.Name, nil
		}
	}
	return "", nil
}

func (s *Session) AppendMessage(ctx context.Context, msg agent.AgentMessage) (string, error) {
	entry := MessageEntry{Message: msg}
	if err := s.storage.AppendEntry(ctx, entry); err != nil {
		return "", err
	}
	leaf, err := s.storage.LeafID(ctx)
	if err != nil || leaf == nil {
		return "", err
	}
	return *leaf, nil
}

func (s *Session) AppendThinkingLevelChange(ctx context.Context, level string) (string, error) {
	return appendAndLeaf(ctx, s.storage, ThinkingLevelChangeEntry{ThinkingLevel: level})
}

func (s *Session) AppendModelChange(ctx context.Context, provider string, modelID string) (string, error) {
	return appendAndLeaf(ctx, s.storage, ModelChangeEntry{Provider: provider, ModelID: modelID})
}

func (s *Session) AppendActiveToolsChange(ctx context.Context, activeToolNames []string) (string, error) {
	return appendAndLeaf(ctx, s.storage, ActiveToolsChangeEntry{ActiveToolNames: append([]string(nil), activeToolNames...)})
}

func (s *Session) AppendCompaction(ctx context.Context, summary string, firstKeptEntryID string, tokensBefore int, details any, fromHook bool) (string, error) {
	return appendAndLeaf(ctx, s.storage, CompactionEntry{
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          details,
		FromHook:         fromHook,
	})
}

func (s *Session) AppendCustomEntry(ctx context.Context, customType string, data any) (string, error) {
	return appendAndLeaf(ctx, s.storage, CustomEntry{CustomType: customType, Data: data})
}

func (s *Session) AppendCustomMessageEntry(ctx context.Context, customType string, content any, display bool, details any) (string, error) {
	return appendAndLeaf(ctx, s.storage, CustomMessageEntry{
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	})
}

func (s *Session) AppendLabel(ctx context.Context, targetID string, label string) (string, error) {
	entry, err := s.storage.Entry(ctx, targetID)
	if err != nil {
		return "", err
	}
	if entry == nil {
		return "", &SessionError{Code: "not_found", Msg: "Entry " + targetID + " not found"}
	}
	return appendAndLeaf(ctx, s.storage, LabelEntry{TargetID: targetID, Label: label})
}

func (s *Session) AppendSessionName(ctx context.Context, name string) (string, error) {
	return appendAndLeaf(ctx, s.storage, SessionInfoEntry{Name: name})
}

type BranchMove struct {
	Summary  string
	Details  any
	FromHook bool
}

func (s *Session) MoveTo(ctx context.Context, entryID *string, moves ...*BranchMove) (string, error) {
	if err := s.storage.SetLeafID(ctx, entryID); err != nil {
		return "", err
	}
	if len(moves) > 0 && moves[0] != nil && moves[0].Summary != "" {
		fromID := ""
		if entryID != nil {
			fromID = *entryID
		} else {
			fromID = "root"
		}
		if err := s.storage.AppendEntry(ctx, BranchSummaryEntry{
			FromID:   fromID,
			Summary:  moves[0].Summary,
			Details:  moves[0].Details,
			FromHook: moves[0].FromHook,
		}); err != nil {
			return "", err
		}
	}
	leaf, err := s.storage.LeafID(ctx)
	if err != nil || leaf == nil {
		return "", err
	}
	return *leaf, nil
}

func appendAndLeaf(ctx context.Context, storage Storage, entry Entry) (string, error) {
	if err := storage.AppendEntry(ctx, entry); err != nil {
		return "", err
	}
	leaf, err := storage.LeafID(ctx)
	if err != nil || leaf == nil {
		return "", err
	}
	return *leaf, nil
}
