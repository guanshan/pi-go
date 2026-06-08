package codingagent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/guanshan/pi-go/packages/ai"
	core "github.com/guanshan/pi-go/packages/coding-agent/core"
)

type SessionFileEntry struct {
	Type                string           `json:"type"`
	Version             int              `json:"version,omitempty"`
	ID                  string           `json:"id,omitempty"`
	Timestamp           string           `json:"timestamp,omitempty"`
	CWD                 string           `json:"cwd,omitempty"`
	ParentSession       string           `json:"parentSession,omitempty"`
	ParentID            *string          `json:"parentId"`
	Message             ai.Message       `json:"message,omitempty"`
	Provider            string           `json:"provider,omitempty"`
	ModelID             string           `json:"modelId,omitempty"`
	ThinkingLevel       ai.ThinkingLevel `json:"thinkingLevel,omitempty"`
	Summary             string           `json:"summary,omitempty"`
	FirstKeptID         string           `json:"firstKeptEntryId,omitempty"`
	FirstKeptEntryIndex *int             `json:"firstKeptEntryIndex,omitempty"`
	TokensBefore        int              `json:"tokensBefore,omitempty"`
	FromID              string           `json:"fromId,omitempty"`
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

func (e *SessionFileEntry) UnmarshalJSON(data []byte) error {
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
	type alias SessionFileEntry
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
	*e = SessionFileEntry(out)
	return nil
}

type SessionModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func ParseSessionEntries(content string) []SessionFileEntry {
	var entries []SessionFileEntry
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry SessionFileEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

func MigrateSessionEntries(entries []SessionFileEntry) {
	version := 1
	for _, entry := range entries {
		if entry.Type == "session" {
			if entry.Version > 0 {
				version = entry.Version
			}
			break
		}
	}
	if version >= CurrentSessionVersion {
		return
	}
	if version < 2 {
		migrateSessionV1ToV2(entries)
	}
	if version < 3 {
		migrateSessionV2ToV3(entries)
	}
}

func GetLatestCompactionEntry(entries []core.SessionEntry) *core.SessionEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "compaction" {
			entry := entries[i]
			return &entry
		}
	}
	return nil
}

func BuildSessionContext(entries []core.SessionEntry, leafID ...*string) core.SessionContext {
	byID := map[string]core.SessionEntry{}
	for _, entry := range entries {
		if entry.ID != "" {
			byID[entry.ID] = entry
		}
	}
	if len(leafID) > 0 && leafID[0] == nil {
		return core.SessionContext{ThinkingLevel: ai.ThinkingOff}
	}
	var leaf *core.SessionEntry
	if len(leafID) > 0 && leafID[0] != nil {
		if entry, ok := byID[*leafID[0]]; ok {
			leaf = &entry
		}
	}
	if leaf == nil && len(entries) > 0 {
		entry := entries[len(entries)-1]
		leaf = &entry
	}
	if leaf == nil {
		return core.SessionContext{ThinkingLevel: ai.ThinkingOff}
	}
	path := sessionPathToRoot(*leaf, byID)
	ctx := core.SessionContext{ThinkingLevel: ai.ThinkingOff}
	var compaction *core.SessionEntry
	for i := range path {
		entry := path[i]
		switch entry.Type {
		case "thinking_level_change":
			if entry.ThinkingLevel != "" {
				ctx.ThinkingLevel = entry.ThinkingLevel
			}
		case "model_change":
			ctx.ModelProvider = entry.Provider
			ctx.ModelID = entry.ModelID
		case "message":
			if assistant, ok := ai.AsAssistantMessage(entry.Message); ok {
				ctx.ModelProvider = assistant.Provider
				ctx.ModelID = assistant.Model
			}
		case "compaction":
			e := entry
			compaction = &e
		case "session_info":
			ctx.Name = entry.Name
		}
	}
	appendEntryMessage := func(entry core.SessionEntry) {
		switch entry.Type {
		case "message":
			if entry.Message != nil {
				ctx.Messages = append(ctx.Messages, entry.Message)
			}
		case "custom_message":
			ctx.Messages = append(ctx.Messages, core.CustomSessionMessage{
				Role:       "custom",
				CustomType: entry.CustomType,
				Content:    entry.Content,
				Display:    entry.Display,
				Details:    entry.Details,
			})
		case "branch_summary":
			if entry.Summary != "" {
				ctx.Messages = append(ctx.Messages, core.BranchSummaryMessage{Role: "branchSummary", Summary: entry.Summary, FromID: entry.FromID})
			}
		}
	}
	if compaction != nil {
		ctx.Messages = append(ctx.Messages, core.CompactionSummaryMessage{
			Role:         "compactionSummary",
			Summary:      compaction.Summary,
			TokensBefore: compaction.TokensBefore,
		})
		compactionIndex := -1
		for i := range path {
			if path[i].ID == compaction.ID {
				compactionIndex = i
				break
			}
		}
		foundFirstKept := false
		for i := 0; i < compactionIndex; i++ {
			if path[i].ID == compaction.FirstKeptID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendEntryMessage(path[i])
			}
		}
		for i := compactionIndex + 1; i < len(path); i++ {
			appendEntryMessage(path[i])
		}
		return ctx
	}
	for _, entry := range path {
		appendEntryMessage(entry)
	}
	return ctx
}

func sessionPathToRoot(leaf core.SessionEntry, byID map[string]core.SessionEntry) []core.SessionEntry {
	var reversed []core.SessionEntry
	current := leaf
	for {
		reversed = append(reversed, current)
		if current.ParentID == nil || *current.ParentID == "" {
			break
		}
		parent, ok := byID[*current.ParentID]
		if !ok {
			break
		}
		current = parent
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

func migrateSessionV1ToV2(entries []SessionFileEntry) {
	seen := map[string]bool{}
	var previous *string
	for i := range entries {
		entry := &entries[i]
		if entry.Type == "session" {
			entry.Version = 2
			continue
		}
		id := uniqueSessionEntryID(seen)
		entry.ID = id
		entry.ParentID = previous
		previous = &entry.ID
		if entry.Type == "compaction" && entry.FirstKeptEntryIndex != nil {
			index := *entry.FirstKeptEntryIndex
			if index >= 0 && index < len(entries) && entries[index].Type != "session" {
				entry.FirstKeptID = entries[index].ID
			}
			entry.FirstKeptEntryIndex = nil
		}
	}
}

func migrateSessionV2ToV3(entries []SessionFileEntry) {
	for i := range entries {
		entry := &entries[i]
		if entry.Type == "session" {
			entry.Version = 3
			continue
		}
		if entry.Type == "message" && ai.MessageRole(entry.Message) == "hookMessage" {
			if custom, ok := ai.AsCustomMessage(entry.Message); ok {
				custom.Role = "custom"
				entry.Message = custom
			}
		}
	}
}

func uniqueSessionEntryID(seen map[string]bool) string {
	for i := 0; i < 101; i++ {
		var b [16]byte
		if _, err := rand.Read(b[:]); err == nil {
			id := hex.EncodeToString(b[:4])
			if i == 100 {
				id = hex.EncodeToString(b[:])
			}
			if !seen[id] {
				seen[id] = true
				return id
			}
		}
	}
	return "session-entry"
}
