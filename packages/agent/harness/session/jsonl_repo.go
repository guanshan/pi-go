package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	harnessenv "github.com/guanshan/pi-go/packages/agent/harness/env"
)

const CurrentSessionVersion = 3

type JSONLHeader struct {
	Type              string `json:"type"`
	Version           int    `json:"version"`
	ID                string `json:"id"`
	Timestamp         string `json:"timestamp"`
	Cwd               string `json:"cwd"`
	ParentSessionPath string `json:"parentSession,omitempty"`
}

type JSONLRepo struct {
	fs           harnessenv.FileSystem
	sessionsRoot string
}

func NewJSONLRepo(fs harnessenv.FileSystem, sessionsRoot string) JSONLRepo {
	return JSONLRepo{fs: fs, sessionsRoot: sessionsRoot}
}

func (r JSONLRepo) Create(ctx context.Context, opts JSONLCreateOptions) (*Session, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	fs, err := r.fileSystem()
	if err != nil {
		return nil, err
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd = fs.Cwd()
	}
	if opts.Path != "" {
		storage, err := CreateJSONLStorageWithFS(ctx, fs, opts.Path, JSONLMetadata{
			Metadata:          Metadata{ID: opts.ID},
			Cwd:               cwd,
			Path:              opts.Path,
			ParentSessionPath: opts.ParentSessionPath,
		})
		if err != nil {
			return nil, err
		}
		return New(storage), nil
	}
	id := opts.ID
	if id == "" {
		id = CreateSessionID()
	}
	createdAt := CreateTimestamp()
	filePath, err := r.createSessionFilePath(ctx, fs, cwd, id, createdAt)
	if err != nil {
		return nil, err
	}
	sessionDir, err := r.sessionDir(ctx, fs, cwd)
	if err != nil {
		return nil, err
	}
	if err := sessionFileError("storage", "failed to create session directory "+sessionDir, fs.CreateDir(ctx, sessionDir, true)); err != nil {
		return nil, err
	}
	storage, err := CreateJSONLStorageWithFS(ctx, fs, filePath, JSONLMetadata{
		Metadata:          Metadata{ID: id, CreatedAt: createdAt},
		Cwd:               cwd,
		Path:              filePath,
		ParentSessionPath: opts.ParentSessionPath,
	})
	if err != nil {
		return nil, err
	}
	return New(storage), nil
}

func (r JSONLRepo) Open(ctx context.Context, meta JSONLMetadata) (*Session, error) {
	if meta.Path == "" {
		return nil, &SessionError{Code: "invalid_session", Msg: "session path is required"}
	}
	fs, err := r.fileSystem()
	if err != nil {
		return nil, err
	}
	exists, err := fs.Exists(ctx, meta.Path)
	if err != nil {
		return nil, sessionFileError("storage", "failed to check session "+meta.Path, err)
	}
	if !exists {
		return nil, &SessionError{Code: "not_found", Msg: "session not found: " + meta.Path}
	}
	storage, err := OpenJSONLStorageWithFS(ctx, fs, meta.Path)
	if err != nil {
		return nil, err
	}
	return New(storage), nil
}

func (r JSONLRepo) List(ctx context.Context, opts JSONLListOptions) ([]JSONLMetadata, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	fs, err := r.fileSystem()
	if err != nil {
		return nil, err
	}
	legacyDirectDir := r.sessionsRoot == "" && opts.Dir != "" && opts.Cwd == ""
	var dirs []string
	if legacyDirectDir {
		dirs = []string{opts.Dir}
	} else if opts.Cwd != "" {
		dir, err := r.sessionDirWithRoot(ctx, fs, opts.Dir, opts.Cwd)
		if err != nil {
			return nil, err
		}
		dirs = []string{dir}
	} else {
		root, err := r.sessionsRootPath(ctx, fs, opts.Dir)
		if err != nil {
			return nil, err
		}
		exists, err := fs.Exists(ctx, root)
		if err != nil {
			return nil, sessionFileError("storage", "failed to check sessions root "+root, err)
		}
		if !exists {
			return nil, nil
		}
		entries, err := fs.ListDir(ctx, root)
		if err != nil {
			return nil, sessionFileError("storage", "failed to list sessions root "+root, err)
		}
		for _, entry := range entries {
			if entry.Kind == harnessenv.FileKindDirectory {
				dirs = append(dirs, entry.Path)
			}
		}
		sort.Strings(dirs)
	}
	sessions := make([]JSONLMetadata, 0)
	for _, dir := range dirs {
		exists, err := fs.Exists(ctx, dir)
		if err != nil {
			return nil, sessionFileError("storage", "failed to check session directory "+dir, err)
		}
		if !exists {
			continue
		}
		entries, err := fs.ListDir(ctx, dir)
		if err != nil {
			return nil, sessionFileError("storage", "failed to list sessions in "+dir, err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
		for _, entry := range entries {
			if entry.Kind == harnessenv.FileKindDirectory || !strings.HasSuffix(entry.Name, ".jsonl") {
				continue
			}
			meta, err := r.LoadMetadata(ctx, entry.Path)
			if err != nil {
				var sessionErr *SessionError
				if errors.As(err, &sessionErr) && sessionErr.Code == "invalid_session" {
					continue
				}
				return nil, err
			}
			sessions = append(sessions, meta)
		}
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return compareCreatedAtDesc(sessions[i].CreatedAt, sessions[j].CreatedAt)
	})
	return sessions, nil
}

func (r JSONLRepo) Fork(ctx context.Context, src JSONLMetadata, opts JSONLForkOptions) (*Session, error) {
	if src.Path == "" {
		return nil, &SessionError{Code: "invalid_session", Msg: "source session path is required"}
	}
	fs, err := r.fileSystem()
	if err != nil {
		return nil, err
	}
	source, err := r.Open(ctx, src)
	if err != nil {
		return nil, err
	}
	forkedEntries, err := entriesForFork(ctx, source.Storage(), opts.EntryID, opts.Position)
	if err != nil {
		return nil, err
	}
	id := opts.ID
	if id == "" {
		id = CreateSessionID()
	}
	cwd := opts.Cwd
	if cwd == "" {
		cwd = src.Cwd
	}
	if cwd == "" {
		cwd = fs.Cwd()
	}
	parent := opts.ParentSessionPath
	if parent == "" {
		parent = src.Path
	}
	path := opts.Path
	createdAt := CreateTimestamp()
	if path == "" {
		sessionDir, err := r.sessionDir(ctx, fs, cwd)
		if err != nil {
			return nil, err
		}
		if err := sessionFileError("storage", "failed to create session directory "+sessionDir, fs.CreateDir(ctx, sessionDir, true)); err != nil {
			return nil, err
		}
		path, err = r.createSessionFilePath(ctx, fs, cwd, id, createdAt)
		if err != nil {
			return nil, err
		}
	}
	storage, err := CreateJSONLStorageWithFS(ctx, fs, path, JSONLMetadata{
		Metadata:          Metadata{ID: id, CreatedAt: createdAt},
		Cwd:               cwd,
		Path:              path,
		ParentSessionPath: parent,
	})
	if err != nil {
		return nil, err
	}
	for _, entry := range forkedEntries {
		if err := storage.AppendEntry(ctx, entry); err != nil {
			return nil, err
		}
	}
	return New(storage), nil
}

func (r JSONLRepo) LoadMetadata(ctx context.Context, path string) (JSONLMetadata, error) {
	fs, err := r.fileSystem()
	if err != nil {
		return JSONLMetadata{}, err
	}
	lines, err := fs.ReadTextLines(ctx, path, 1)
	if err != nil {
		return JSONLMetadata{}, sessionFileError("storage", "failed to read session header "+path, err)
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return JSONLMetadata{}, invalidSession(path, "missing session header", nil)
	}
	header, err := parseHeaderLine(lines[0], path)
	if err != nil {
		return JSONLMetadata{}, err
	}
	return headerToJSONLMetadata(header, path), nil
}

func (r JSONLRepo) Load(ctx context.Context, path string) (JSONLHeader, []Entry, error) {
	if err := ctxErr(ctx); err != nil {
		return JSONLHeader{}, nil, err
	}
	fs, err := r.fileSystem()
	if err != nil {
		return JSONLHeader{}, nil, err
	}
	content, err := fs.ReadTextFile(ctx, path)
	if err != nil {
		return JSONLHeader{}, nil, sessionFileError("storage", "failed to read session "+path, err)
	}
	lines := nonEmptyLines(content)
	if len(lines) == 0 {
		return JSONLHeader{}, nil, invalidSession(path, "missing session header", nil)
	}
	header, err := parseHeaderLine(lines[0], path)
	if err != nil {
		return JSONLHeader{}, nil, err
	}
	entries := make([]Entry, 0, len(lines)-1)
	for i := 1; i < len(lines); i++ {
		entry, err := parseEntryLine(lines[i], path, i+1)
		if err != nil {
			return JSONLHeader{}, nil, err
		}
		entries = append(entries, entry)
	}
	return header, entries, nil
}

func (r JSONLRepo) Save(ctx context.Context, path string, header JSONLHeader, entries []Entry) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if path == "" {
		return &SessionError{Code: "invalid_session", Msg: "session path is required"}
	}
	fs, err := r.fileSystem()
	if err != nil {
		return err
	}
	header = normalizeHeader(header, fs.Cwd())
	var out []byte
	line, err := marshalJSONLine(header)
	if err != nil {
		return err
	}
	out = append(out, line...)
	for _, entry := range entries {
		line, err := marshalJSONLine(marshalEntry(entry))
		if err != nil {
			return err
		}
		out = append(out, line...)
	}
	return sessionFileError("storage", "failed to save session "+path, fs.WriteFile(ctx, path, out))
}

func (r JSONLRepo) Append(ctx context.Context, path string, entry Entry) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if path == "" {
		return &SessionError{Code: "invalid_session", Msg: "session path is required"}
	}
	fs, err := r.fileSystem()
	if err != nil {
		return err
	}
	line, err := marshalJSONLine(marshalEntry(entry))
	if err != nil {
		return err
	}
	return sessionFileError("storage", "failed to append session entry "+entry.EntryID(), fs.AppendFile(ctx, path, line))
}

func (r JSONLRepo) Delete(ctx context.Context, meta JSONLMetadata) error {
	return r.DeletePath(ctx, meta.Path)
}

func (r JSONLRepo) DeletePath(ctx context.Context, path string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	fs, err := r.fileSystem()
	if err != nil {
		return err
	}
	return sessionFileError("storage", "failed to delete session "+path, fs.Remove(ctx, path, false, true))
}

func (r JSONLRepo) fileSystem() (harnessenv.FileSystem, error) {
	if r.fs != nil {
		return r.fs, nil
	}
	env, err := harnessenv.NewLocalExecutionEnv("", "", nil)
	if err != nil {
		return nil, err
	}
	return env, nil
}

func (r JSONLRepo) sessionsRootPath(ctx context.Context, fs harnessenv.FileSystem, override string) (string, error) {
	root := override
	if root == "" {
		root = r.sessionsRoot
	}
	if root == "" {
		return "", &SessionError{Code: "invalid_session", Msg: "session directory is required"}
	}
	resolved, err := fs.AbsolutePath(ctx, root)
	if err != nil {
		return "", sessionFileError("storage", "failed to resolve sessions root "+root, err)
	}
	return resolved, nil
}

func (r JSONLRepo) sessionDir(ctx context.Context, fs harnessenv.FileSystem, cwd string) (string, error) {
	return r.sessionDirWithRoot(ctx, fs, "", cwd)
}

func (r JSONLRepo) sessionDirWithRoot(ctx context.Context, fs harnessenv.FileSystem, rootOverride string, cwd string) (string, error) {
	root, err := r.sessionsRootPath(ctx, fs, rootOverride)
	if err != nil {
		return "", err
	}
	path, err := fs.JoinPath(ctx, []string{root, encodeCwd(cwd)})
	if err != nil {
		return "", sessionFileError("storage", "failed to resolve session directory for "+cwd, err)
	}
	return path, nil
}

func (r JSONLRepo) createSessionFilePath(ctx context.Context, fs harnessenv.FileSystem, cwd string, sessionID string, timestamp string) (string, error) {
	dir, err := r.sessionDir(ctx, fs, cwd)
	if err != nil {
		return "", err
	}
	name := strings.NewReplacer(":", "-", ".", "-").Replace(timestamp) + "_" + sessionID + ".jsonl"
	path, err := fs.JoinPath(ctx, []string{dir, name})
	if err != nil {
		return "", sessionFileError("storage", "failed to resolve session file path for "+sessionID, err)
	}
	return path, nil
}

func encodeCwd(cwd string) string {
	cwd = strings.TrimLeft(cwd, `/\`)
	replacer := strings.NewReplacer("/", "-", `\`, "-", ":", "-")
	return "--" + replacer.Replace(cwd) + "--"
}

func normalizeHeader(header JSONLHeader, defaultCwd string) JSONLHeader {
	if header.Type == "" {
		header.Type = "session"
	}
	if header.Version == 0 {
		header.Version = CurrentSessionVersion
	}
	if header.ID == "" {
		header.ID = CreateSessionID()
	}
	if header.Timestamp == "" {
		header.Timestamp = CreateTimestamp()
	}
	if header.Cwd == "" {
		header.Cwd = defaultCwd
	}
	return header
}

func parseHeaderLine(line string, path string) (JSONLHeader, error) {
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return JSONLHeader{}, invalidSession(path, "first line is not a valid session header", err)
	}
	if raw["type"] != "session" {
		return JSONLHeader{}, invalidSession(path, "first line is not a valid session header", nil)
	}
	version, ok := raw["version"].(float64)
	if !ok || int(version) != CurrentSessionVersion {
		return JSONLHeader{}, invalidSession(path, "unsupported session version", nil)
	}
	id, ok := raw["id"].(string)
	if !ok || id == "" {
		return JSONLHeader{}, invalidSession(path, "session header is missing id", nil)
	}
	timestamp, ok := raw["timestamp"].(string)
	if !ok || timestamp == "" {
		return JSONLHeader{}, invalidSession(path, "session header is missing timestamp", nil)
	}
	cwd, ok := raw["cwd"].(string)
	if !ok || cwd == "" {
		return JSONLHeader{}, invalidSession(path, "session header is missing cwd", nil)
	}
	parent := ""
	if value, ok := raw["parentSession"]; ok {
		parent, ok = value.(string)
		if !ok {
			return JSONLHeader{}, invalidSession(path, "session header parentSession must be a string", nil)
		}
	} else if value, ok := raw["parentSessionPath"]; ok {
		parent, ok = value.(string)
		if !ok {
			return JSONLHeader{}, invalidSession(path, "session header parentSessionPath must be a string", nil)
		}
	}
	return JSONLHeader{Type: "session", Version: CurrentSessionVersion, ID: id, Timestamp: timestamp, Cwd: cwd, ParentSessionPath: parent}, nil
}

func parseEntryLine(line string, path string, lineNumber int) (Entry, error) {
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, invalidEntry(path, lineNumber, "is not valid JSON", err)
	}
	entryType, ok := raw["type"].(string)
	if !ok || entryType == "" {
		return nil, invalidEntry(path, lineNumber, "is missing entry type", nil)
	}
	id, ok := raw["id"].(string)
	if !ok || id == "" {
		return nil, invalidEntry(path, lineNumber, "is missing entry id", nil)
	}
	if parent, ok := raw["parentId"]; ok && parent != nil {
		if _, ok := parent.(string); !ok {
			return nil, invalidEntry(path, lineNumber, "has invalid parentId", nil)
		}
	}
	timestamp, ok := raw["timestamp"].(string)
	if !ok || timestamp == "" {
		return nil, invalidEntry(path, lineNumber, "is missing timestamp", nil)
	}
	if entryType == "leaf" {
		if target, ok := raw["targetId"]; ok && target != nil {
			if _, ok := target.(string); !ok {
				return nil, invalidEntry(path, lineNumber, "has invalid targetId", nil)
			}
		}
	}
	entry, err := unmarshalEntry([]byte(line))
	if err != nil {
		var sessionErr *SessionError
		if errors.As(err, &sessionErr) {
			return nil, err
		}
		return nil, invalidEntry(path, lineNumber, "is not a valid session entry", err)
	}
	return entry, nil
}

func invalidSession(path string, message string, cause error) error {
	return &SessionError{Code: "invalid_session", Msg: "invalid JSONL session file " + path + ": " + message, Err: cause}
}

func invalidEntry(path string, lineNumber int, message string, cause error) error {
	return &SessionError{Code: "invalid_entry", Msg: fmt.Sprintf("invalid JSONL session file %s: line %d %s", path, lineNumber, message), Err: cause}
}

func sessionFileError(defaultCode string, message string, err error) error {
	if err == nil {
		return nil
	}
	var sessionErr *SessionError
	if errors.As(err, &sessionErr) {
		return err
	}
	var fileErr *harnessenv.FileError
	if errors.As(err, &fileErr) {
		code := defaultCode
		if fileErr.Code == harnessenv.FileErrNotFound {
			code = "not_found"
		}
		return &SessionError{Code: code, Msg: message + ": " + fileErr.Error(), Err: err}
	}
	return &SessionError{Code: defaultCode, Msg: message + ": " + err.Error(), Err: err}
}

func nonEmptyLines(content string) []string {
	raw := strings.Split(content, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func headerToJSONLMetadata(header JSONLHeader, path string) JSONLMetadata {
	return JSONLMetadata{
		Metadata:          Metadata{ID: header.ID, CreatedAt: header.Timestamp},
		Cwd:               header.Cwd,
		Path:              path,
		ParentSessionPath: header.ParentSessionPath,
	}
}

func compareCreatedAtDesc(a string, b string) bool {
	at, aErr := time.Parse(time.RFC3339Nano, a)
	bt, bErr := time.Parse(time.RFC3339Nano, b)
	if aErr == nil && bErr == nil {
		return at.After(bt)
	}
	return a > b
}
