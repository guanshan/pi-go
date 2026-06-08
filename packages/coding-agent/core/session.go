package core

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guanshan/pi-go/packages/ai"
)

type SessionHeader struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

type SessionEntry struct {
	Type          string           `json:"type"`
	ID            string           `json:"id,omitempty"`
	ParentID      *string          `json:"parentId"`
	Timestamp     string           `json:"timestamp,omitempty"`
	Message       ai.Message       `json:"message,omitempty"`
	Provider      string           `json:"provider,omitempty"`
	ModelID       string           `json:"modelId,omitempty"`
	ThinkingLevel ai.ThinkingLevel `json:"thinkingLevel,omitempty"`
	Summary       string           `json:"summary,omitempty"`
	FirstKeptID   string           `json:"firstKeptEntryId,omitempty"`
	// FirstKeptEntryIndex only exists in legacy v1 compaction entries; the
	// v1→v2 migration converts it to FirstKeptID and clears it so it is never
	// re-serialized.
	FirstKeptEntryIndex *int            `json:"firstKeptEntryIndex,omitempty"`
	TokensBefore        int             `json:"tokensBefore,omitempty"`
	FromID              string          `json:"fromId,omitempty"`
	CustomType          string          `json:"customType,omitempty"`
	Data                json.RawMessage `json:"data,omitempty"`
	Content             any             `json:"content,omitempty"`
	Display             bool            `json:"display,omitempty"`
	Details             any             `json:"details,omitempty"`
	FromHook            bool            `json:"fromHook,omitempty"`
	FromHookSet         bool            `json:"-"`
	TargetID            string          `json:"targetId,omitempty"`
	Label               string          `json:"label,omitempty"`
	Name                string          `json:"name,omitempty"`
	Raw                 json.RawMessage `json:"-"`
}

type sessionEntryRecord struct {
	Type                string           `json:"type"`
	ID                  string           `json:"id,omitempty"`
	ParentID            *string          `json:"parentId"`
	Timestamp           string           `json:"timestamp,omitempty"`
	Message             ai.Message       `json:"message,omitempty"`
	Provider            string           `json:"provider,omitempty"`
	ModelID             string           `json:"modelId,omitempty"`
	ThinkingLevel       ai.ThinkingLevel `json:"thinkingLevel,omitempty"`
	FromID              string           `json:"fromId,omitempty"`
	Summary             string           `json:"summary,omitempty"`
	FirstKeptID         string           `json:"firstKeptEntryId,omitempty"`
	FirstKeptEntryIndex *int             `json:"firstKeptEntryIndex,omitempty"`
	TokensBefore        int              `json:"tokensBefore,omitempty"`
	CustomType          string           `json:"customType,omitempty"`
	Data                json.RawMessage  `json:"data,omitempty"`
	Content             any              `json:"content,omitempty"`
	Display             bool             `json:"display,omitempty"`
	Details             any              `json:"details,omitempty"`
	FromHook            bool             `json:"fromHook,omitempty"`
	TargetID            string           `json:"targetId,omitempty"`
	Label               string           `json:"label,omitempty"`
	Name                string           `json:"name,omitempty"`
}

func (e SessionEntry) MarshalJSON() ([]byte, error) {
	record := sessionEntryRecord{
		Type:                e.Type,
		ID:                  e.ID,
		ParentID:            e.ParentID,
		Timestamp:           e.Timestamp,
		Message:             e.Message,
		Provider:            e.Provider,
		ModelID:             e.ModelID,
		ThinkingLevel:       e.ThinkingLevel,
		FromID:              e.FromID,
		Summary:             e.Summary,
		FirstKeptID:         e.FirstKeptID,
		FirstKeptEntryIndex: e.FirstKeptEntryIndex,
		TokensBefore:        e.TokensBefore,
		CustomType:          e.CustomType,
		Data:                e.Data,
		Content:             e.Content,
		Display:             e.Display,
		Details:             e.Details,
		FromHook:            e.FromHook,
		TargetID:            e.TargetID,
		Label:               e.Label,
		Name:                e.Name,
	}
	data, err := marshalJSONNoHTMLEscape(record)
	if err != nil {
		return nil, err
	}
	switch e.Type {
	case "compaction":
		if e.TokensBefore == 0 {
			data = insertJSONFieldAfterKey(data, "firstKeptEntryId", `"tokensBefore":0`)
		}
		if !e.FromHook && e.FromHookSet {
			data = appendJSONFieldBeforeClose(data, `"fromHook":false`)
		}
	case "branch_summary":
		if !e.FromHook && e.FromHookSet {
			data = appendJSONFieldBeforeClose(data, `"fromHook":false`)
		}
	case "custom_message":
		if !e.Display {
			data = insertJSONFieldAfterKey(data, "content", `"display":false`)
		}
	}
	return data, nil
}

func (e *SessionEntry) UnmarshalJSON(data []byte) error {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	messageRaw := fields["message"]
	delete(fields, "message")
	stripped, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type alias SessionEntry
	var out alias
	if err := json.Unmarshal(stripped, &out); err != nil {
		return err
	}
	if len(messageRaw) > 0 && string(messageRaw) != "null" {
		msg, err := ai.UnmarshalMessageJSON(messageRaw)
		if err != nil {
			return err
		}
		out.Message = msg
	}
	_, out.FromHookSet = fields["fromHook"]
	*e = SessionEntry(out)
	return nil
}

type SessionInfo struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	CWD       string    `json:"cwd"`
	Name      string    `json:"name,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SessionManager struct {
	mu        sync.RWMutex
	Header    SessionHeader
	Path      string
	InMemory  bool
	Entries   []SessionEntry
	CurrentID *string
	// cwdOverride, when set, replaces the stored Header.CWD at runtime without
	// rewriting the session file. Used when the recorded cwd no longer exists and
	// the user chooses to continue in the current directory (mirrors TS using
	// MissingSessionCwdIssue.fallbackCwd as the runtime cwd while leaving the
	// session file's cwd intact).
	cwdOverride string
}

type SessionContext struct {
	Messages      []ai.Message
	ModelProvider string
	ModelID       string
	ThinkingLevel ai.ThinkingLevel
	Name          string
}

func NewSessionManager(cwd, sessionDir string) (*SessionManager, error) {
	return NewSessionManagerWithID(cwd, sessionDir, uuid())
}

// ValidSessionID reports whether id is a valid explicit session id, mirroring
// the TypeScript assertValidSessionId rule: non-empty, only alphanumerics plus
// '-', '_', '.', and starting and ending with an alphanumeric character.
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

func NewSessionManagerWithID(cwd, sessionDir, id string) (*SessionManager, error) {
	if id == "" {
		id = uuid()
	}
	path := filepath.Join(sessionDir, encodeCWD(cwd), fmt.Sprintf("%s_%s.jsonl", time.Now().Format("20060102T150405"), id))
	sm := &SessionManager{
		Header: SessionHeader{
			Type:      "session",
			Version:   CurrentSessionVersion,
			ID:        id,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			CWD:       cwd,
		},
		Path: path,
	}
	if err := sm.writeHeader(); err != nil {
		return nil, err
	}
	return sm, nil
}

func InMemorySession(cwd string) *SessionManager {
	return &SessionManager{
		InMemory: true,
		Header: SessionHeader{
			Type:      "session",
			Version:   CurrentSessionVersion,
			ID:        uuid(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			CWD:       cwd,
		},
	}
}

// loadSessionFile reads a session JSONL file tolerantly: blank lines are
// skipped, malformed (non-JSON) entry lines are skipped instead of aborting the
// load, and the first valid `session` line is taken as the header. It mirrors
// the TypeScript loadEntriesFromFile and does NOT migrate or rewrite the file.
func loadSessionFile(path, fallbackCWD string) (SessionHeader, []SessionEntry, error) {
	path = ExpandTilde(path)
	f, err := os.Open(path)
	if err != nil {
		return SessionHeader{}, nil, err
	}
	defer f.Close()

	// Read line by line with bufio.Reader instead of bufio.Scanner so a single
	// entry larger than Scanner's max token size (a big base64 image or paste)
	// does not make the whole session unopenable. Mirrors the TypeScript reader,
	// which loads sessions larger than Node's max string length.
	reader := bufio.NewReader(f)
	var header SessionHeader
	haveHeader := false
	var entries []SessionEntry
	for {
		line, readErr := reader.ReadBytes('\n')
		line = trimLineEnding(line)
		if len(line) > 0 || readErr == nil {
			if len(strings.TrimSpace(string(line))) != 0 {
				if !haveHeader {
					var h SessionHeader
					if err := json.Unmarshal(line, &h); err == nil && h.Type == "session" {
						header = h
						if header.CWD == "" {
							header.CWD = fallbackCWD
						}
						haveHeader = true
					}
					// A non-header first line is malformed/legacy noise: skip it
					// and keep scanning for the header instead of failing.
				} else if entry, ok := parseSessionEntry(line); ok {
					entries = append(entries, entry)
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return SessionHeader{}, nil, readErr
		}
	}
	if !haveHeader || header.Type == "" {
		return SessionHeader{}, nil, errors.New("invalid session: missing header")
	}
	return header, entries, nil
}

// trimLineEnding strips a trailing "\n" (and a preceding "\r" if present) so a
// line read via bufio.Reader.ReadBytes('\n') matches bufio.Scanner.Bytes(),
// which excludes the newline.
func trimLineEnding(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
	}
	return line
}

// parseSessionEntry decodes a single non-header JSONL entry line. Malformed
// lines (e.g. a partially written final entry after a crash) are skipped rather
// than discarding the entire session. The returned entry copies the raw bytes
// because ReadBytes reuses no buffer but callers retain entry.Raw.
func parseSessionEntry(line []byte) (SessionEntry, bool) {
	var entry SessionEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return SessionEntry{}, false
	}
	entry.Raw = append([]byte(nil), line...)
	return entry, true
}

// OpenSession opens a session as the active session. It loads tolerantly,
// migrates the entries to the current on-disk version and rewrites the file
// when a migration changed anything, matching the TypeScript open/setSessionFile
// behaviour.
func OpenSession(path string, fallbackCWD string) (*SessionManager, error) {
	header, entries, err := loadSessionFile(path, fallbackCWD)
	if err != nil {
		return nil, err
	}
	sm := &SessionManager{Header: header, Path: ExpandTilde(path), Entries: entries}
	changed := migrateSessionEntries(&sm.Header, sm.Entries)
	sm.recomputeCurrentID()
	if changed {
		if _, err := sm.rewrite(); err != nil {
			return nil, err
		}
	}
	return sm, nil
}

// openSessionNoRewrite loads and in-memory-migrates a session without rewriting
// the source file. Use it for read-only consumers (export) and clone sources
// (fork/import) that must not mutate the file they read.
func openSessionNoRewrite(path, fallbackCWD string) (*SessionManager, error) {
	header, entries, err := loadSessionFile(path, fallbackCWD)
	if err != nil {
		return nil, err
	}
	sm := &SessionManager{Header: header, Path: ExpandTilde(path), Entries: entries}
	migrateSessionEntries(&sm.Header, sm.Entries)
	sm.recomputeCurrentID()
	return sm, nil
}

// recomputeCurrentID sets CurrentID to the last tree entry that carries an id,
// reflecting the leaf of the active branch after a (possibly migrating) load.
func (s *SessionManager) recomputeCurrentID() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CurrentID = nil
	for i := range s.Entries {
		if s.Entries[i].ID != "" && treeEntry(s.Entries[i].Type) {
			id := s.Entries[i].ID
			s.CurrentID = &id
		}
	}
}

// findLocalSessionByExactID returns the path of the session in cwd's session
// directory whose id matches exactly, or "" if none. It mirrors the TypeScript
// findLocalSessionByExactId helper used to resolve --session-id and
// --fork --session-id.
func findLocalSessionByExactID(cwd, sessionDir, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	sessions, err := ListSessions(cwd, sessionDir)
	if err != nil {
		return "", err
	}
	for _, info := range sessions {
		if info.ID == id {
			return info.Path, nil
		}
	}
	return "", nil
}

func ForkSession(sourcePath, cwd, sessionDir string) (*SessionManager, error) {
	return ForkSessionWithID(sourcePath, cwd, sessionDir, "")
}

// ForkSessionWithID forks sourcePath into cwd's session directory. When id is
// non-empty the new session uses it as its explicit id (caller is responsible
// for rejecting collisions), otherwise a random id is generated.
func ForkSessionWithID(sourcePath, cwd, sessionDir, id string) (*SessionManager, error) {
	// Load (and migrate in memory) the source without rewriting it: forking
	// must not mutate the session being forked from, matching TS forkFrom.
	source, err := openSessionNoRewrite(sourcePath, cwd)
	if err != nil {
		return nil, err
	}
	target, err := NewSessionManagerWithID(cwd, sessionDir, id)
	if err != nil {
		return nil, err
	}
	target.Header.ParentSession = sourcePath
	target.Entries = append(target.Entries, source.Branch()...)
	target.CurrentID = nil
	for i := range target.Entries {
		if target.Entries[i].ID != "" && treeEntry(target.Entries[i].Type) {
			id := target.Entries[i].ID
			target.CurrentID = &id
		}
	}
	return target.rewrite()
}

func (s *SessionManager) CWD() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cwdOverride != "" {
		return s.cwdOverride
	}
	return s.Header.CWD
}

// OverrideCWD makes CWD() report cwd at runtime without persisting it to the
// session file. Used to continue a session whose recorded cwd no longer exists.
func (s *SessionManager) OverrideCWD(cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cwdOverride = cwd
}

func (s *SessionManager) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.Header.ID
}

func (s *SessionManager) File() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.InMemory {
		return ""
	}
	return s.Path
}

func (s *SessionManager) writeHeader() error {
	s.mu.RLock()
	inMemory := s.InMemory
	path := s.Path
	header := s.Header
	s.mu.RUnlock()

	if inMemory {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeJSONLine(file, header)
}

func (s *SessionManager) rewrite() (*SessionManager, error) {
	s.mu.RLock()
	inMemory := s.InMemory
	path := s.Path
	header := s.Header
	entries := append([]SessionEntry(nil), s.Entries...)
	s.mu.RUnlock()

	if inMemory {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := writeJSONLine(file, header); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := writeJSONLine(file, entry); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *SessionManager) Append(entry SessionEntry) error {
	if entry.ID == "" && treeEntry(entry.Type) {
		entry.ID = shortID()
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	if treeEntry(entry.Type) {
		if s.CurrentID != nil {
			parent := *s.CurrentID
			entry.ParentID = &parent
		} else {
			entry.ParentID = nil
		}
		id := entry.ID
		s.CurrentID = &id
	}
	if entry.Type == "compaction" || entry.Type == "branch_summary" {
		entry.FromHookSet = true
	}
	s.Entries = append(s.Entries, entry)
	inMemory := s.InMemory
	path := s.Path
	s.mu.Unlock()

	if inMemory {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeJSONLine(file, entry)
}

func (s *SessionManager) AppendMessage(msg ai.Message) error {
	return s.Append(SessionEntry{Type: "message", Message: msg})
}

func (s *SessionManager) AppendModelChange(provider, modelID string) error {
	return s.Append(SessionEntry{Type: "model_change", Provider: provider, ModelID: modelID})
}

func (s *SessionManager) AppendThinkingChange(level ai.ThinkingLevel) error {
	return s.Append(SessionEntry{Type: "thinking_level_change", ThinkingLevel: level})
}

func (s *SessionManager) AppendSessionName(name string) error {
	return s.Append(SessionEntry{Type: "session_info", Name: name})
}

// EntriesSnapshot returns a copy of the session entries taken under the read
// lock, so callers can read the tree without racing a concurrent Append.
func (s *SessionManager) EntriesSnapshot() []SessionEntry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]SessionEntry(nil), s.Entries...)
}

// CurrentLeafID returns the id of the active branch leaf ("" when the session
// is empty) under the read lock.
func (s *SessionManager) CurrentLeafID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.CurrentID == nil {
		return ""
	}
	return *s.CurrentID
}

// Snapshot returns a consistent copy of the session header, entries, and current
// leaf pointer under the read lock.
func (s *SessionManager) Snapshot() (SessionHeader, []SessionEntry, *string) {
	if s == nil {
		return SessionHeader{}, nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	header := s.Header
	entries := append([]SessionEntry(nil), s.Entries...)
	var leaf *string
	if s.CurrentID != nil {
		id := *s.CurrentID
		leaf = &id
	}
	return header, entries, leaf
}

func (s *SessionManager) HasEntry(id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.Entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func (s *SessionManager) Branch() []SessionEntry {
	s.mu.RLock()
	if s.CurrentID == nil {
		s.mu.RUnlock()
		return nil
	}
	currentID := *s.CurrentID
	entries := append([]SessionEntry(nil), s.Entries...)
	s.mu.RUnlock()

	byID := map[string]SessionEntry{}
	for _, entry := range entries {
		if entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	var branch []SessionEntry
	id := currentID
	for id != "" {
		entry, ok := byID[id]
		if !ok {
			break
		}
		branch = append(branch, entry)
		if entry.ParentID == nil {
			break
		}
		id = *entry.ParentID
	}
	for i, j := 0, len(branch)-1; i < j; i, j = i+1, j-1 {
		branch[i], branch[j] = branch[j], branch[i]
	}
	return branch
}

func (s *SessionManager) BuildContext() SessionContext {
	ctx := SessionContext{ThinkingLevel: ai.ThinkingOff}
	branch := s.Branch()
	var compaction *SessionEntry
	compactionIndex := -1
	for i := range branch {
		entry := branch[i]
		switch entry.Type {
		case "model_change":
			ctx.ModelProvider = entry.Provider
			ctx.ModelID = entry.ModelID
		case "thinking_level_change":
			if entry.ThinkingLevel != "" {
				ctx.ThinkingLevel = entry.ThinkingLevel
			}
		case "session_info":
			ctx.Name = entry.Name
		case "compaction":
			entryCopy := entry
			compaction = &entryCopy
			compactionIndex = i
		case "message":
			if assistant, ok := ai.AsAssistantMessage(entry.Message); ok && assistant.Provider != "" && assistant.Model != "" {
				ctx.ModelProvider = assistant.Provider
				ctx.ModelID = assistant.Model
			}
		}
	}
	if compaction != nil {
		ctx.Messages = append(ctx.Messages, compactionSummaryMessage(*compaction))
		foundFirstKept := false
		for i := 0; i < compactionIndex; i++ {
			entry := branch[i]
			if entry.ID == compaction.FirstKeptID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendContextEntryMessage(&ctx, entry)
			}
		}
		for i := compactionIndex + 1; i < len(branch); i++ {
			appendContextEntryMessage(&ctx, branch[i])
		}
		return ctx
	}
	for _, entry := range branch {
		appendContextEntryMessage(&ctx, entry)
	}
	return ctx
}

func sessionBranchHasThinkingChange(session *SessionManager) bool {
	if session == nil {
		return false
	}
	for _, entry := range session.Branch() {
		if entry.Type == "thinking_level_change" {
			return true
		}
	}
	return false
}

func appendContextEntryMessage(ctx *SessionContext, entry SessionEntry) {
	switch entry.Type {
	case "message":
		if entry.Message != nil {
			ctx.Messages = append(ctx.Messages, entry.Message)
		}
	case "branch_summary":
		if entry.Summary != "" {
			ctx.Messages = append(ctx.Messages, BranchSummaryMessage{
				Role:        "branchSummary",
				Summary:     entry.Summary,
				FromID:      entry.FromID,
				TimestampMs: entryTimestampMs(entry),
			})
		}
	case "custom_message":
		ctx.Messages = append(ctx.Messages, CustomSessionMessage{
			Role:        "custom",
			CustomType:  entry.CustomType,
			Content:     entry.Content,
			Display:     entry.Display,
			Details:     entry.Details,
			TimestampMs: entryTimestampMs(entry),
		})
	}
}

func compactionSummaryMessage(entry SessionEntry) ai.Message {
	return CompactionSummaryMessage{
		Role:         "compactionSummary",
		Summary:      entry.Summary,
		TokensBefore: entry.TokensBefore,
		TimestampMs:  entryTimestampMs(entry),
	}
}

func entryTimestampMs(entry SessionEntry) int64 {
	if entry.Timestamp == "" {
		return time.Now().UnixMilli()
	}
	parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return parsed.UnixMilli()
}

func (s *SessionManager) Stats() map[string]any {
	ctx := s.BuildContext()
	stats := map[string]any{
		"sessionFile":  s.File(),
		"sessionId":    s.SessionID(),
		"messageCount": len(ctx.Messages),
		"sessionName":  ctx.Name,
	}
	var user, assistant, tools int
	var usage ai.Usage
	for _, msg := range ctx.Messages {
		switch ai.MessageRole(msg) {
		case "user":
			user++
		case "assistant":
			assistant++
			assistantMessage, _ := ai.AsAssistantMessage(msg)
			usage.Input += assistantMessage.Usage.Input
			usage.Output += assistantMessage.Usage.Output
			usage.CacheRead += assistantMessage.Usage.CacheRead
			usage.CacheWrite += assistantMessage.Usage.CacheWrite
			usage.TotalTokens += assistantMessage.Usage.TotalTokens
			usage.Cost.Input += assistantMessage.Usage.Cost.Input
			usage.Cost.Output += assistantMessage.Usage.Cost.Output
			usage.Cost.CacheRead += assistantMessage.Usage.Cost.CacheRead
			usage.Cost.CacheWrite += assistantMessage.Usage.Cost.CacheWrite
			usage.Cost.Total += assistantMessage.Usage.Cost.Total
		case "toolResult":
			tools++
		}
	}
	stats["userMessages"] = user
	stats["assistantMessages"] = assistant
	stats["toolResults"] = tools
	stats["tokens"] = usage
	return stats
}

func ListSessions(cwd, sessionDir string) ([]SessionInfo, error) {
	root := filepath.Join(sessionDir, encodeCWD(cwd))
	return listSessionFiles(root)
}

func ListAllSessions(sessionDir string) ([]SessionInfo, error) {
	var out []SessionInfo
	if _, err := os.Stat(sessionDir); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(sessionDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := readSessionInfo(path)
		if err == nil {
			out = append(out, info)
		}
		return nil
	})
	sortSessions(out)
	return out, err
}

func listSessionFiles(root string) ([]SessionInfo, error) {
	var out []SessionInfo
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := readSessionInfo(filepath.Join(root, entry.Name()))
		if err == nil {
			out = append(out, info)
		}
	}
	sortSessions(out)
	return out, nil
}

func sortSessions(sessions []SessionInfo) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

func readSessionInfo(path string) (SessionInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer file.Close()
	stat, _ := file.Stat()
	// Use bufio.Reader rather than bufio.Scanner so a session with an entry
	// larger than Scanner's max token size still lists correctly (see
	// loadSessionFile).
	reader := bufio.NewReader(file)
	var header SessionHeader
	var preview, name string
	var lastActivityMs int64
	first := true
	for {
		line, readErr := reader.ReadBytes('\n')
		line = trimLineEnding(line)
		if len(line) > 0 || readErr == nil {
			if first {
				first = false
				if err := json.Unmarshal(line, &header); err != nil {
					return SessionInfo{}, err
				}
			} else if entry, ok := parseSessionEntry(line); ok {
				if entry.Type == "session_info" && entry.Name != "" {
					name = entry.Name
				}
				if entry.Message != nil {
					role := ai.MessageRole(entry.Message)
					if (role == "user" || role == "assistant") && entry.Message.Timestamp() > lastActivityMs {
						lastActivityMs = entry.Message.Timestamp()
					}
					if preview == "" && role == "user" {
						preview = ai.MessageText(entry.Message)
						// Truncate by rune, not byte, so a non-ASCII first
						// message never yields invalid UTF-8 in the preview.
						if runes := []rune(preview); len(runes) > 120 {
							preview = string(runes[:120]) + "..."
						}
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return SessionInfo{}, readErr
		}
	}
	if stat == nil {
		stat, _ = os.Stat(path)
	}
	updated := time.Time{}
	if stat != nil {
		updated = stat.ModTime()
	}
	if headerTime, err := time.Parse(time.RFC3339Nano, header.Timestamp); err == nil {
		updated = headerTime
	}
	if lastActivityMs > 0 {
		updated = time.UnixMilli(lastActivityMs)
	}
	return SessionInfo{ID: header.ID, Path: path, CWD: header.CWD, Name: name, Preview: preview, UpdatedAt: updated}, nil
}

func ContinueRecent(cwd, sessionDir string) (*SessionManager, error) {
	sessions, err := ListSessions(cwd, sessionDir)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return NewSessionManager(cwd, sessionDir)
	}
	return OpenSession(sessions[0].Path, cwd)
}

// ResolvedSessionType classifies how a --session/--fork argument was resolved,
// mirroring the TS ResolvedSession union (main.ts:137-141).
type ResolvedSessionType string

const (
	// ResolvedSessionPathType is a direct file path argument.
	ResolvedSessionPathType ResolvedSessionType = "path"
	// ResolvedSessionLocal is an ID match within the current project.
	ResolvedSessionLocal ResolvedSessionType = "local"
	// ResolvedSessionGlobal is an ID match in a different project (cross-project).
	ResolvedSessionGlobal ResolvedSessionType = "global"
	// ResolvedSessionNotFound means no match anywhere.
	ResolvedSessionNotFound ResolvedSessionType = "not_found"
)

// ResolvedSession is the typed result of resolving a session argument. CWD is
// the origin project's cwd, set only for global (cross-project) matches.
type ResolvedSession struct {
	Type ResolvedSessionType
	Path string
	CWD  string
	Arg  string
}

// ResolveSession resolves a --session/--fork argument to a typed result. It
// reports whether a match is a direct path, a local (current-project) session,
// a global (cross-project) session, or not found — matching TS resolveSessionPath
// (main.ts:158-183). Matching prefers an exact ID, then an ID prefix.
func ResolveSession(arg, cwd, sessionDir string) ResolvedSession {
	if strings.ContainsAny(arg, `/\`) || strings.HasSuffix(arg, ".jsonl") {
		expanded := ExpandTilde(arg)
		if filepath.IsAbs(expanded) {
			return ResolvedSession{Type: ResolvedSessionPathType, Path: expanded, Arg: arg}
		}
		return ResolvedSession{Type: ResolvedSessionPathType, Path: ResolveInCWD(cwd, arg), Arg: arg}
	}
	local, _ := ListSessions(cwd, sessionDir)
	if match, ok := matchSessionByID(local, arg); ok {
		return ResolvedSession{Type: ResolvedSessionLocal, Path: match.Path, Arg: arg}
	}
	all, _ := ListAllSessions(sessionDir)
	if match, ok := matchSessionByID(all, arg); ok {
		return ResolvedSession{Type: ResolvedSessionGlobal, Path: match.Path, CWD: match.CWD, Arg: arg}
	}
	return ResolvedSession{Type: ResolvedSessionNotFound, Arg: arg}
}

// matchSessionByID returns the session whose ID exactly equals arg, falling back
// to the first session whose ID has arg as a prefix (mirroring TS).
func matchSessionByID(sessions []SessionInfo, arg string) (SessionInfo, bool) {
	for _, s := range sessions {
		if s.ID == arg {
			return s, true
		}
	}
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, arg) {
			return s, true
		}
	}
	return SessionInfo{}, false
}

// ResolveSessionPath resolves a session argument to a file path. It is retained
// for callers (e.g. --fork) that treat path/local/global matches identically;
// see ResolveSession for the type-aware variant.
func ResolveSessionPath(arg, cwd, sessionDir string) (string, error) {
	resolved := ResolveSession(arg, cwd, sessionDir)
	if resolved.Type == ResolvedSessionNotFound {
		return "", fmt.Errorf("no session found matching %q", arg)
	}
	return resolved.Path, nil
}

// writeJSONLine serializes a single session JSONL record without HTML-escaping
// `<`, `>`, `&`. TS persists session entries via `JSON.stringify`, which does not
// escape those characters; Go's default json.Marshal does, which would make the
// on-disk session bytes diverge for common code payloads (HTML, `&&`,
// `List<String>`). Mirrors writeRPCJSONLine on the RPC path.
func writeJSONLine(w io.Writer, value any) error {
	raw, err := marshalJSONStringifyLine(value)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func treeEntry(typ string) bool {
	switch typ {
	case "message", "model_change", "thinking_level_change", "compaction", "branch_summary", "custom", "custom_message", "label", "session_info":
		return true
	default:
		return false
	}
}

func encodeCWD(cwd string) string {
	clean := filepath.Clean(cwd)
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, `\`) {
		clean = clean[1:]
	}
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + repl.Replace(clean) + "--"
}

func uuid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func shortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
