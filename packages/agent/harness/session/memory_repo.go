package session

import (
	"context"
	"sync"
)

type MemoryRepo struct {
	mu       sync.Mutex
	sessions map[string]*MemoryStorage
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{sessions: map[string]*MemoryStorage{}}
}

func (r *MemoryRepo) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	if r.sessions == nil {
		r.sessions = map[string]*MemoryStorage{}
	}
	storage, err := NewMemoryStorage(Metadata{ID: opts.ID}, nil)
	if err != nil {
		return nil, err
	}
	meta, err := storage.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.sessions[meta.ID] = storage
	r.mu.Unlock()
	return New(storage), nil
}

func (r *MemoryRepo) Open(ctx context.Context, meta Metadata) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	storage := r.sessions[meta.ID]
	if storage == nil {
		return nil, &SessionError{Code: "not_found", Msg: "session " + meta.ID + " not found"}
	}
	return New(storage), nil
}

func (r *MemoryRepo) Delete(ctx context.Context, meta Metadata) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, meta.ID)
	return nil
}

func (r *MemoryRepo) Fork(ctx context.Context, source Metadata, opts ForkOptions) (*Session, error) {
	src, err := r.Open(ctx, source)
	if err != nil {
		return nil, err
	}
	forkedEntries, err := entriesForFork(ctx, src.Storage(), opts.EntryID, opts.Position)
	if err != nil {
		return nil, err
	}
	storage, err := NewMemoryStorage(Metadata{ID: opts.ID}, forkedEntries)
	if err != nil {
		return nil, err
	}
	meta, err := storage.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.sessions == nil {
		r.sessions = map[string]*MemoryStorage{}
	}
	r.sessions[meta.ID] = storage
	r.mu.Unlock()
	return New(storage), nil
}

func (r *MemoryRepo) List(ctx context.Context, opts MemoryListOptions) ([]Metadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Metadata, 0, len(r.sessions))
	for _, storage := range r.sessions {
		meta, err := storage.Metadata(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, nil
}
