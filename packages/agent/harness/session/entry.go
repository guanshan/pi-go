package session

import (
	"time"

	"github.com/guanshan/pi-go/packages/agent"
)

type Entry interface {
	EntryType() string
	EntryID() string
	EntryParentID() *string
	EntryTimestamp() string
}

type BaseEntry struct {
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

func (e BaseEntry) EntryID() string        { return e.ID }
func (e BaseEntry) EntryParentID() *string { return cloneStringPtr(e.ParentID) }
func (e BaseEntry) EntryTimestamp() string { return e.Timestamp }
func (e *BaseEntry) ensure(id string, parentID *string) {
	if e.ID == "" {
		e.ID = id
	}
	if e.Timestamp == "" {
		e.Timestamp = CreateTimestamp()
	}
	if e.ParentID == nil && parentID != nil {
		e.ParentID = cloneStringPtr(parentID)
	}
}

type MessageEntry struct {
	BaseEntry
	Message agent.AgentMessage `json:"message"`
}

func (MessageEntry) EntryType() string { return "message" }

type ThinkingLevelChangeEntry struct {
	BaseEntry
	ThinkingLevel string `json:"thinkingLevel"`
}

func (ThinkingLevelChangeEntry) EntryType() string { return "thinking_level_change" }

type ModelChangeEntry struct {
	BaseEntry
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func (ModelChangeEntry) EntryType() string { return "model_change" }

type ActiveToolsChangeEntry struct {
	BaseEntry
	ActiveToolNames []string `json:"activeToolNames"`
}

func (ActiveToolsChangeEntry) EntryType() string { return "active_tools_change" }

type CompactionEntry struct {
	BaseEntry
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	TokensBefore     int    `json:"tokensBefore"`
	Details          any    `json:"details,omitempty"`
	FromHook         bool   `json:"fromHook,omitempty"`
}

func (CompactionEntry) EntryType() string { return "compaction" }

type BranchSummaryEntry struct {
	BaseEntry
	FromID   string `json:"fromId"`
	Summary  string `json:"summary"`
	Details  any    `json:"details,omitempty"`
	FromHook bool   `json:"fromHook,omitempty"`
}

func (BranchSummaryEntry) EntryType() string { return "branch_summary" }

type CustomEntry struct {
	BaseEntry
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitempty"`
}

func (CustomEntry) EntryType() string { return "custom" }

type CustomMessageEntry struct {
	BaseEntry
	CustomType string `json:"customType"`
	Content    any    `json:"content,omitempty"`
	Details    any    `json:"details,omitempty"`
	Display    bool   `json:"display,omitempty"`
}

func (CustomMessageEntry) EntryType() string { return "custom_message" }

type LabelEntry struct {
	BaseEntry
	TargetID string `json:"targetId"`
	Label    string `json:"label"`
}

func (LabelEntry) EntryType() string { return "label" }

type SessionInfoEntry struct {
	BaseEntry
	Name string `json:"name"`
}

func (SessionInfoEntry) EntryType() string { return "session_info" }

type LeafEntry struct {
	BaseEntry
	TargetID *string `json:"targetId,omitempty"`
}

func (LeafEntry) EntryType() string { return "leaf" }

func CreateTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
