package core

import (
	"fmt"
	"sort"
	"strings"
)

func (s *SessionManager) ResolveEntryID(query string) (string, error) {
	return resolveEntryIDFromEntries(s.EntriesSnapshot(), query)
}

func resolveEntryIDFromEntries(entries []SessionEntry, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("entry id is required")
	}
	var matches []string
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		if entry.ID == query {
			return entry.ID, nil
		}
		if strings.HasPrefix(entry.ID, query) {
			matches = append(matches, entry.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("entry %q not found", query)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("entry id %q is ambiguous", query)
	}
}

func (s *SessionManager) SetLeaf(entryID string) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}
	entryID = strings.TrimSpace(entryID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryID == "" {
		s.CurrentID = nil
		return nil
	}
	resolved, err := resolveEntryIDFromEntries(s.Entries, entryID)
	if err != nil {
		return err
	}
	s.CurrentID = &resolved
	return nil
}

func (s *SessionManager) BranchFrom(entryID string) ([]SessionEntry, error) {
	if s == nil {
		return nil, fmt.Errorf("session is nil")
	}
	entries := s.EntriesSnapshot()
	return branchFromEntries(entries, entryID)
}

func branchFromEntries(entries []SessionEntry, entryID string) ([]SessionEntry, error) {
	resolved, err := resolveEntryIDFromEntries(entries, entryID)
	if err != nil {
		return nil, err
	}
	return branchFromResolvedEntries(entries, resolved)
}

func branchFromResolvedEntries(entries []SessionEntry, resolved string) ([]SessionEntry, error) {
	byID := map[string]SessionEntry{}
	for _, entry := range entries {
		if entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	var branch []SessionEntry
	seen := map[string]bool{}
	id := resolved
	for id != "" {
		if seen[id] {
			return nil, fmt.Errorf("cycle detected at entry %s", id)
		}
		seen[id] = true
		entry, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("entry %s not found", id)
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
	return branch, nil
}

func CloneSessionBranch(source *SessionManager, leafID string, sessionDir string) (*SessionManager, error) {
	if source == nil {
		return nil, fmt.Errorf("session is nil")
	}
	if leafID == "" {
		leafID = source.CurrentLeafID()
		if leafID == "" {
			return nil, fmt.Errorf("nothing to clone yet")
		}
	}
	branch, err := source.BranchFrom(leafID)
	if err != nil {
		return nil, err
	}
	target, err := NewSessionManager(source.CWD(), sessionDir)
	if err != nil {
		return nil, err
	}
	if source.File() != "" {
		target.Header.ParentSession = source.File()
	}
	target.Entries = append(target.Entries, branch...)
	target.CurrentID = nil
	for i := range target.Entries {
		if target.Entries[i].ID != "" && treeEntry(target.Entries[i].Type) {
			id := target.Entries[i].ID
			target.CurrentID = &id
		}
	}
	return target.rewrite()
}

func FormatSessionTree(s *SessionManager) string {
	_, entries, leaf := s.Snapshot()
	if len(entries) == 0 {
		return "No entries in session\n"
	}
	children := map[string][]SessionEntry{}
	byID := map[string]SessionEntry{}
	var roots []SessionEntry
	for _, entry := range entries {
		if entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		parent := ""
		if entry.ParentID != nil {
			parent = *entry.ParentID
		}
		if parent == "" || parent == entry.ID {
			roots = append(roots, entry)
			continue
		}
		if _, ok := byID[parent]; !ok {
			roots = append(roots, entry)
			continue
		}
		children[parent] = append(children[parent], entry)
	}
	sortSessionTreeEntries(roots)
	for parent := range children {
		sortSessionTreeEntries(children[parent])
	}

	current := ""
	if leaf != nil {
		current = *leaf
	}
	var b strings.Builder
	b.WriteString("Session tree:\n")
	visited := map[string]bool{}
	for _, root := range roots {
		renderSessionTreeEntry(&b, root, children, current, "", visited)
	}
	return b.String()
}

func sortSessionTreeEntries(entries []SessionEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Timestamp == entries[j].Timestamp {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Timestamp < entries[j].Timestamp
	})
}

func renderSessionTreeEntry(b *strings.Builder, entry SessionEntry, children map[string][]SessionEntry, current string, indent string, visited map[string]bool) {
	if visited[entry.ID] {
		return
	}
	visited[entry.ID] = true
	marker := " "
	if entry.ID == current {
		marker = "*"
	}
	fmt.Fprintf(b, "%s%s %s %-13s %s\n", indent, marker, entry.ID, exportEntryRole(entry), exportEntryPreview(entry))
	nextIndent := indent + "  "
	for _, child := range children[entry.ID] {
		renderSessionTreeEntry(b, child, children, current, nextIndent, visited)
	}
}
