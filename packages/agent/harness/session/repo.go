package session

import (
	"context"

	"github.com/guanshan/pi-go/packages/ai"
)

type CreateOptions struct {
	ID string
}

type ForkOptions struct {
	CreateOptions
	EntryID  string
	Position string
}

type Repo interface {
	Create(context.Context, CreateOptions) (*Session, error)
	Open(context.Context, Metadata) (*Session, error)
	Delete(context.Context, Metadata) error
	Fork(context.Context, Metadata, ForkOptions) (*Session, error)
}

type JSONLCreateOptions struct {
	CreateOptions
	Path              string
	Cwd               string
	ParentSessionPath string
}

type JSONLListOptions struct {
	Dir string
	Cwd string
}

type JSONLForkOptions struct {
	ForkOptions
	Path              string
	Cwd               string
	ParentSessionPath string
}

type MemoryListOptions struct{}

func entriesForFork(ctx context.Context, storage Storage, entryID string, position string) ([]Entry, error) {
	if entryID == "" {
		return storage.Entries(ctx)
	}
	target, err := storage.Entry(ctx, entryID)
	if err != nil {
		return nil, &SessionError{Code: "invalid_fork_target", Msg: "entry " + entryID + " not found", Err: err}
	}
	effectiveLeafID := entryID
	if position != "at" {
		message, ok := target.(MessageEntry)
		if !ok || ai.MessageRole(message.Message) != "user" {
			return nil, &SessionError{Code: "invalid_fork_target", Msg: "entry " + entryID + " is not a user message"}
		}
		if parent := target.EntryParentID(); parent != nil {
			effectiveLeafID = *parent
		} else {
			return nil, nil
		}
	}
	return storage.PathToRoot(ctx, &effectiveLeafID)
}
