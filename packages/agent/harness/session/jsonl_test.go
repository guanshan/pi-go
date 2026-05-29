package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
	"github.com/guanshan/pi-go/packages/ai"
)

func TestJSONLStorageRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	storage, err := CreateJSONLStorage(ctx, path, JSONLMetadata{
		Metadata: Metadata{ID: "s1", CreatedAt: "2026-05-28T00:00:00Z"},
		Cwd:      "/work",
		Path:     path,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := New(storage)
	firstID, err := sess.AppendMessage(ctx, ai.NewUserMessage("one", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendModelChange(ctx, "openai", "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendThinkingLevelChange(ctx, "medium"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendActiveToolsChange(ctx, []string{"lookup"}); err != nil {
		t.Fatal(err)
	}
	secondID, err := sess.AppendMessage(ctx, ai.NewUserMessage("two", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &secondID); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenJSONLStorage(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	reopenedSession := New(reopened)
	built, err := reopenedSession.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || ai.MessageText(built.Messages[0]) != "one" || ai.MessageText(built.Messages[1]) != "two" {
		t.Fatalf("context=%#v", built)
	}
	if built.Model == nil || built.Model.Provider != "openai" || built.ThinkingLevel != "medium" {
		t.Fatalf("state=%#v", built)
	}
	if built.ActiveToolNames == nil || len(*built.ActiveToolNames) != 1 || (*built.ActiveToolNames)[0] != "lookup" {
		t.Fatalf("active tools=%#v", built.ActiveToolNames)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("lines=%q", raw)
	}
	var header JSONLHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Version != CurrentSessionVersion || header.ID != "s1" || header.Cwd != "/work" {
		t.Fatalf("header=%#v", header)
	}
}

func TestJSONLRepoRejectsUnknownEntryType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	err := os.WriteFile(path, []byte(`{"type":"session","version":3,"id":"s","timestamp":"now"}`+"\n"+`{"type":"mystery","id":"x"}`+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = (JSONLRepo{}).Load(context.Background(), path)
	if err == nil {
		t.Fatal("expected load error")
	}
}

func TestJSONLRepoCreateListForkDelete(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := JSONLRepo{}
	path := filepath.Join(dir, "one.jsonl")
	sess, err := repo.Create(ctx, JSONLCreateOptions{CreateOptions: CreateOptions{ID: "one"}, Path: path, Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := sess.AppendMessage(ctx, ai.NewUserMessage("one", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("two", nil)); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, JSONLListOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "one" || list[0].Path != path {
		t.Fatalf("list=%#v", list)
	}
	forkPath := filepath.Join(dir, "fork.jsonl")
	fork, err := repo.Fork(ctx, list[0], JSONLForkOptions{
		ForkOptions: ForkOptions{CreateOptions: CreateOptions{ID: "fork"}, EntryID: firstID, Position: "at"},
		Path:        forkPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := fork.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 1 || ai.MessageText(built.Messages[0]) != "one" {
		t.Fatalf("fork context=%#v", built)
	}
	if err := repo.Delete(ctx, JSONLMetadata{Path: forkPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(forkPath); !os.IsNotExist(err) {
		t.Fatalf("fork still exists err=%v", err)
	}
}

func TestJSONLRepoUsesFileSystemSessionRootCwdAndFork(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	env, err := harnessenv.NewLocalExecutionEnv(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewJSONLRepo(env, "sessions")
	sess, err := repo.Create(ctx, JSONLCreateOptions{
		CreateOptions: CreateOptions{ID: "one"},
		Cwd:           "/Users/me/work:repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := sess.AppendMessage(ctx, ai.NewUserMessage("one", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("two", nil)); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, JSONLListOptions{Cwd: "/Users/me/work:repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "one" || list[0].Cwd != "/Users/me/work:repo" {
		t.Fatalf("list=%#v", list)
	}
	if !strings.Contains(list[0].Path, "--Users-me-work-repo--") {
		t.Fatalf("path did not encode cwd: %s", list[0].Path)
	}
	fork, err := repo.Fork(ctx, list[0], JSONLForkOptions{
		ForkOptions: ForkOptions{CreateOptions: CreateOptions{ID: "fork"}, EntryID: firstID, Position: "at"},
		Cwd:         "/tmp/fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := fork.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 1 || ai.MessageText(built.Messages[0]) != "one" {
		t.Fatalf("fork context=%#v", built)
	}
	forks, err := repo.List(ctx, JSONLListOptions{Cwd: "/tmp/fork"})
	if err != nil {
		t.Fatal(err)
	}
	if len(forks) != 1 || forks[0].ParentSessionPath != list[0].Path {
		t.Fatalf("forks=%#v source=%#v", forks, list)
	}
	all, err := repo.List(ctx, JSONLListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all sessions=%#v", all)
	}
}

func TestJSONLRepoForkBeforeRequiresUserMessage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := JSONLRepo{}
	path := filepath.Join(dir, "one.jsonl")
	sess, err := repo.Create(ctx, JSONLCreateOptions{CreateOptions: CreateOptions{ID: "one"}, Path: path, Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := sess.AppendMessage(ctx, ai.NewAssistantMessageForModel(ai.Model{}, ai.TextBlocks("answer"), ai.Usage{}, "stop"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Fork(ctx, JSONLMetadata{Path: path}, JSONLForkOptions{
		ForkOptions: ForkOptions{CreateOptions: CreateOptions{ID: "fork"}, EntryID: assistantID, Position: "before"},
		Path:        filepath.Join(dir, "fork.jsonl"),
	})
	var sessionErr *SessionError
	if !errors.As(err, &sessionErr) || sessionErr.Code != "invalid_fork_target" {
		t.Fatalf("err=%#v sessionErr=%#v", err, sessionErr)
	}
}

func TestJSONLRepoForkUsesTreePath(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := JSONLRepo{}
	path := filepath.Join(dir, "one.jsonl")
	sess, err := repo.Create(ctx, JSONLCreateOptions{CreateOptions: CreateOptions{ID: "one"}, Path: path, Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	rootID, err := sess.AppendMessage(ctx, ai.NewUserMessage("root", nil))
	if err != nil {
		t.Fatal(err)
	}
	leftID, err := sess.AppendMessage(ctx, ai.NewUserMessage("left", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.MoveTo(ctx, &rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(ctx, ai.NewUserMessage("right", nil)); err != nil {
		t.Fatal(err)
	}
	fork, err := repo.Fork(ctx, JSONLMetadata{Path: path}, JSONLForkOptions{
		ForkOptions: ForkOptions{CreateOptions: CreateOptions{ID: "fork"}, EntryID: leftID, Position: "at"},
		Path:        filepath.Join(dir, "fork.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := fork.BuildContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Messages) != 2 || ai.MessageText(built.Messages[0]) != "root" || ai.MessageText(built.Messages[1]) != "left" {
		t.Fatalf("fork context=%#v", built)
	}
}
