package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSelectSessionReadsUserChoice(t *testing.T) {
	sessions := []SessionChoice{
		{ID: "a", Path: "/tmp/a.jsonl", Preview: "first", UpdatedAt: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)},
		{ID: "b", Path: "/tmp/b.jsonl", Name: "second", UpdatedAt: time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC)},
	}
	var out bytes.Buffer
	path, err := SelectSession(strings.NewReader("2\n"), &out, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/b.jsonl" {
		t.Fatalf("path=%q", path)
	}
	if !strings.Contains(out.String(), "Select session [1-2]") {
		t.Fatalf("missing prompt:\n%s", out.String())
	}
}

func TestSelectSessionCancel(t *testing.T) {
	_, err := SelectSession(strings.NewReader("q\n"), ioDiscard{}, []SessionChoice{
		{ID: "a", Path: "/tmp/a.jsonl", UpdatedAt: time.Now()},
		{ID: "b", Path: "/tmp/b.jsonl", UpdatedAt: time.Now()},
	})
	if !errors.Is(err, ErrSessionSelectionCancelled) {
		t.Fatalf("err=%v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
