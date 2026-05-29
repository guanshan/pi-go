package session

import "context"

type Metadata struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
}

type JSONLMetadata struct {
	Metadata
	Cwd               string `json:"cwd"`
	Path              string `json:"path"`
	ParentSessionPath string `json:"parentSessionPath,omitempty"`
}

type Storage interface {
	Metadata(context.Context) (Metadata, error)
	LeafID(context.Context) (*string, error)
	SetLeafID(context.Context, *string) error
	CreateEntryID(context.Context) (string, error)
	AppendEntry(context.Context, Entry) error
	Entry(context.Context, string) (Entry, error)
	FindEntries(context.Context, string) ([]Entry, error)
	Label(context.Context, string) (string, bool)
	PathToRoot(context.Context, *string) ([]Entry, error)
	Entries(context.Context) ([]Entry, error)
}
