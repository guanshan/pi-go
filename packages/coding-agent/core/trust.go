package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Project-trust subsystem — a faithful port of the upstream TypeScript
// core/trust-manager.ts (ProjectTrustStore + hasProjectTrustInputs). When a cwd
// has "trust inputs" (a .pi dir, AGENTS.md/CLAUDE.md, or .agents/skills) and no
// stored decision, the host prompts the user before loading project-scoped
// settings.json, extensions, skills, prompts, and themes. Untrusted projects
// skip all project-scoped resources (see SettingsManager + LoadResources).

// trustContextFileNames mirrors trust-manager.ts CONTEXT_FILE_NAMES.
var trustContextFileNames = []string{"AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}

// canonicalizeTrustPath mirrors trust-manager.ts normalizeCwd =
// canonicalizePath(resolvePath(cwd)): make the path absolute and resolve
// symlinks so the same directory always maps to the same trust-store key.
func canonicalizeTrustPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// HasProjectTrustInputs reports whether cwd looks like a project that should be
// gated behind a trust decision. Mirrors trust-manager.ts hasProjectTrustInputs:
// a .pi directory at cwd, or — walking up to the filesystem root — any of the
// context files (AGENTS.md/CLAUDE.md, case variants) or a .agents/skills dir.
func HasProjectTrustInputs(cwd string) bool {
	currentDir := canonicalizeTrustPath(cwd)
	if dirExists(filepath.Join(currentDir, ConfigDirName)) {
		return true
	}
	for {
		for _, name := range trustContextFileNames {
			if pathExists(filepath.Join(currentDir, name)) {
				return true
			}
		}
		if dirExists(filepath.Join(currentDir, ".agents", "skills")) {
			return true
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			return false
		}
		currentDir = parent
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ProjectTrustStore persists per-cwd trust decisions to <agentDir>/trust.json,
// matching the upstream ProjectTrustStore. Decisions are tri-state: a *bool of
// true/false is a recorded decision; nil means "unknown" (no decision yet).
type ProjectTrustStore struct {
	trustPath string
	mu        sync.Mutex
}

// NewProjectTrustStore returns a store backed by <agentDir>/trust.json.
func NewProjectTrustStore(agentDir string) *ProjectTrustStore {
	return &ProjectTrustStore{trustPath: filepath.Join(agentDir, "trust.json")}
}

// Get returns the recorded decision for cwd, or nil when unknown/unrecorded.
func (s *ProjectTrustStore) Get(cwd string) (*bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := readTrustFile(s.trustPath)
	if err != nil {
		return nil, err
	}
	if v, ok := data[canonicalizeTrustPath(cwd)]; ok && v != nil {
		return v, nil
	}
	return nil, nil
}

// Set records (or, with a nil decision, deletes) the trust decision for cwd.
func (s *ProjectTrustStore) Set(cwd string, decision *bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := readTrustFile(s.trustPath)
	if err != nil {
		return err
	}
	key := canonicalizeTrustPath(cwd)
	if decision == nil {
		delete(data, key)
	} else {
		data[key] = decision
	}
	return writeTrustFile(s.trustPath, data)
}

// readTrustFile parses trust.json into a map of cwd -> decision. Missing file =>
// empty map. Values must be true, false, or null (mirrors the TS validator).
func readTrustFile(path string) (map[string]*bool, error) {
	data := map[string]*bool{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read trust store %s: %w", path, err)
	}
	var parsed map[string]*bool
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("invalid trust store %s: %w", path, err)
	}
	for k, v := range parsed {
		data[k] = v
	}
	return data, nil
}

// writeTrustFile writes trust.json with sorted keys, 2-space indentation, and a
// trailing newline, matching the TS writeTrustFile output byte-for-byte. Only
// true/false decisions are stored (Set deletes on nil), so no null is emitted.
func writeTrustFile(path string, data map[string]*bool) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}
