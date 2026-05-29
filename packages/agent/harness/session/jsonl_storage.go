package session

import (
	"context"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
)

type JSONLStorage struct {
	*MemoryStorage
	path string
	repo JSONLRepo
}

func OpenJSONLStorage(ctx context.Context, path string) (*JSONLStorage, error) {
	fs, err := defaultJSONLFileSystem()
	if err != nil {
		return nil, err
	}
	return OpenJSONLStorageWithFS(ctx, fs, path)
}

func OpenJSONLStorageWithFS(ctx context.Context, fs harnessenv.FileSystem, path string) (*JSONLStorage, error) {
	repo := NewJSONLRepo(fs, "")
	header, entries, err := repo.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	memory, err := NewMemoryStorage(Metadata{ID: header.ID, CreatedAt: header.Timestamp}, entries)
	if err != nil {
		return nil, err
	}
	return &JSONLStorage{MemoryStorage: memory, path: path, repo: repo}, nil
}

func CreateJSONLStorage(ctx context.Context, path string, metadata JSONLMetadata) (*JSONLStorage, error) {
	fs, err := defaultJSONLFileSystem()
	if err != nil {
		return nil, err
	}
	return CreateJSONLStorageWithFS(ctx, fs, path, metadata)
}

func CreateJSONLStorageWithFS(ctx context.Context, fs harnessenv.FileSystem, path string, metadata JSONLMetadata) (*JSONLStorage, error) {
	repo := NewJSONLRepo(fs, "")
	if metadata.ID == "" {
		metadata.ID = CreateSessionID()
	}
	if metadata.CreatedAt == "" {
		metadata.CreatedAt = CreateTimestamp()
	}
	if metadata.Cwd == "" {
		metadata.Cwd = fs.Cwd()
	}
	if metadata.Path == "" {
		metadata.Path = path
	}
	header := JSONLHeader{
		Type:              "session",
		Version:           CurrentSessionVersion,
		ID:                metadata.ID,
		Timestamp:         metadata.CreatedAt,
		Cwd:               metadata.Cwd,
		ParentSessionPath: metadata.ParentSessionPath,
	}
	if err := repo.Save(ctx, path, header, nil); err != nil {
		return nil, err
	}
	return OpenJSONLStorageWithFS(ctx, fs, path)
}

func (s *JSONLStorage) AppendEntry(ctx context.Context, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prepared := ensureEntry(entry, func() string {
		id, _ := s.createEntryIDLocked()
		return id
	}, s.leafID)
	if err := s.repo.Append(ctx, s.path, prepared); err != nil {
		return err
	}
	s.entries = append(s.entries, prepared)
	if id := prepared.EntryID(); id != "" {
		s.byID[id] = prepared
	}
	s.updateLabelLocked(prepared)
	s.leafID = leafIDAfterEntry(prepared)
	return nil
}

func (s *JSONLStorage) SetLeafID(ctx context.Context, leafID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leafID != nil {
		if _, ok := s.byID[*leafID]; !ok {
			return &SessionError{Code: "not_found", Msg: "entry " + *leafID + " not found"}
		}
	}
	entry := ensureEntry(LeafEntry{TargetID: cloneStringPtr(leafID)}, func() string {
		id, _ := s.createEntryIDLocked()
		return id
	}, s.leafID)
	if err := s.repo.Append(ctx, s.path, entry); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	if id := entry.EntryID(); id != "" {
		s.byID[id] = entry
	}
	s.leafID = leafIDAfterEntry(entry)
	return nil
}

func (s *JSONLStorage) Path() string {
	return s.path
}

func defaultJSONLFileSystem() (harnessenv.FileSystem, error) {
	env, err := harnessenv.NewLocalExecutionEnv("", "", nil)
	if err != nil {
		return nil, err
	}
	return env, nil
}
