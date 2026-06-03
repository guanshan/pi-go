package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type MemoryStorage struct {
	mu       sync.RWMutex
	metadata Metadata
	entries  []Entry
	byID     map[string]Entry
	labels   map[string]string
	leafID   *string
}

func NewMemoryStorage(metadata Metadata, entries []Entry) (*MemoryStorage, error) {
	if metadata.ID == "" {
		metadata.ID = CreateSessionID()
	}
	if metadata.CreatedAt == "" {
		metadata.CreatedAt = CreateTimestamp()
	}
	storage := &MemoryStorage{
		metadata: metadata,
		byID:     map[string]Entry{},
		labels:   map[string]string{},
	}
	for _, entry := range entries {
		if err := storage.appendEntryLocked(entry); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

func (s *MemoryStorage) Metadata(context.Context) (Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metadata, nil
}

func (s *MemoryStorage) LeafID(context.Context) (*string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneStringPtr(s.leafID), nil
}

func (s *MemoryStorage) SetLeafID(ctx context.Context, leafID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leafID != nil {
		if _, ok := s.byID[*leafID]; !ok {
			return &SessionError{Code: "not_found", Msg: fmt.Sprintf("entry %s not found", *leafID)}
		}
	}
	id, err := s.createEntryIDLocked()
	if err != nil {
		return err
	}
	return s.appendEntryLocked(LeafEntry{BaseEntry: BaseEntry{ID: id, ParentID: cloneStringPtr(s.leafID), Timestamp: CreateTimestamp()}, TargetID: cloneStringPtr(leafID)})
}

func (s *MemoryStorage) CreateEntryID(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createEntryIDLocked()
}

func (s *MemoryStorage) AppendEntry(ctx context.Context, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendEntryLocked(entry)
}

func (s *MemoryStorage) Entry(ctx context.Context, id string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id], nil
}

func (s *MemoryStorage) FindEntries(ctx context.Context, entryType string) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Entry
	for _, entry := range s.entries {
		if entry.EntryType() == entryType {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (s *MemoryStorage) Label(ctx context.Context, id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	label, ok := s.labels[id]
	return label, ok
}

func (s *MemoryStorage) PathToRoot(ctx context.Context, leafID *string) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if leafID == nil {
		return nil, nil
	}
	var path []Entry
	seen := map[string]bool{}
	currentID := *leafID
	// TS getPathToRoot distinguishes a missing leaf ("not_found") from a missing
	// parent in the chain ("invalid_session"). The first lookup is the leaf; any
	// subsequent missing entry is a broken parent link.
	isLeaf := true
	for currentID != "" {
		if seen[currentID] {
			return nil, &SessionError{Code: "invalid_session", Msg: fmt.Sprintf("cycle detected at entry %s", currentID)}
		}
		seen[currentID] = true
		entry := s.byID[currentID]
		if entry == nil {
			if isLeaf {
				return nil, &SessionError{Code: "not_found", Msg: fmt.Sprintf("entry %s not found", currentID)}
			}
			return nil, &SessionError{Code: "invalid_session", Msg: fmt.Sprintf("entry %s not found", currentID)}
		}
		isLeaf = false
		path = append(path, entry)
		parent := entry.EntryParentID()
		if parent == nil {
			break
		}
		currentID = *parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

func (s *MemoryStorage) Entries(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.entries...), nil
}

func (s *MemoryStorage) createEntryIDLocked() (string, error) {
	for i := 0; i < 100; i++ {
		id := UUIDv7()
		short := id
		if len(short) > 8 {
			short = short[:8]
		}
		if _, ok := s.byID[short]; !ok {
			return short, nil
		}
	}
	return UUIDv7(), nil
}

func (s *MemoryStorage) appendEntryLocked(entry Entry) error {
	if entry == nil {
		return &SessionError{Code: "invalid_entry", Msg: "entry is nil"}
	}
	entry = ensureEntry(entry, func() string {
		id, _ := s.createEntryIDLocked()
		return id
	}, s.leafID)
	s.entries = append(s.entries, entry)
	if id := entry.EntryID(); id != "" {
		s.byID[id] = entry
	}
	s.updateLabelLocked(entry)
	s.leafID = leafIDAfterEntry(entry)
	return nil
}

func (s *MemoryStorage) updateLabelLocked(entry Entry) {
	label, ok := entry.(LabelEntry)
	if !ok {
		return
	}
	value := strings.TrimSpace(label.Label)
	if value == "" {
		delete(s.labels, label.TargetID)
		return
	}
	s.labels[label.TargetID] = value
}

func ensureEntry(entry Entry, nextID func() string, parentID *string) Entry {
	switch e := entry.(type) {
	case MessageEntry:
		e.ensure(nextID(), parentID)
		return e
	case ThinkingLevelChangeEntry:
		e.ensure(nextID(), parentID)
		return e
	case ModelChangeEntry:
		e.ensure(nextID(), parentID)
		return e
	case ActiveToolsChangeEntry:
		e.ensure(nextID(), parentID)
		return e
	case CompactionEntry:
		e.ensure(nextID(), parentID)
		return e
	case BranchSummaryEntry:
		e.ensure(nextID(), parentID)
		return e
	case CustomEntry:
		e.ensure(nextID(), parentID)
		return e
	case CustomMessageEntry:
		e.ensure(nextID(), parentID)
		return e
	case LabelEntry:
		e.ensure(nextID(), parentID)
		return e
	case SessionInfoEntry:
		e.ensure(nextID(), parentID)
		return e
	case LeafEntry:
		e.ensure(nextID(), parentID)
		return e
	default:
		return entry
	}
}

func leafIDAfterEntry(entry Entry) *string {
	if leaf, ok := entry.(LeafEntry); ok {
		return cloneStringPtr(leaf.TargetID)
	}
	if entry.EntryID() == "" {
		return nil
	}
	id := entry.EntryID()
	return &id
}
