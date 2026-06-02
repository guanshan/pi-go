package session

import (
	"encoding/json"

	"github.com/guanshan/pi-go/packages/ai"
)

type entryRecord struct {
	Type      string     `json:"type"`
	ID        string     `json:"id,omitempty"`
	ParentID  *string    `json:"parentId"`
	Timestamp string     `json:"timestamp,omitempty"`
	Message   ai.Message `json:"message,omitempty"`
	// FromID precedes Summary so branch_summary entries serialize fields in the
	// order TS writes them ({fromId, summary}; session.ts:260-261). Compaction
	// entries leave FromID empty (omitempty), so their {summary, firstKeptEntryId,
	// tokensBefore} order is unaffected.
	FromID           string          `json:"fromId,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	CustomType       string          `json:"customType,omitempty"`
	Data             any             `json:"data,omitempty"`
	Content          any             `json:"content,omitempty"`
	Display          bool            `json:"display,omitempty"`
	Details          any             `json:"details,omitempty"`
	FromHook         bool            `json:"fromHook,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	ModelID          string          `json:"modelId,omitempty"`
	ActiveToolNames  []string        `json:"activeToolNames,omitempty"`
	ThinkingLevel    string          `json:"thinkingLevel,omitempty"`
	TargetID         *string         `json:"targetId,omitempty"`
	Label            string          `json:"label,omitempty"`
	Name             string          `json:"name,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

// MarshalJSON serializes an entry record. Two shared, omitempty fields must be
// re-added for specific entry types because the TypeScript parser/reader require
// them present even when empty:
//   - Leaf entries must always carry an explicit targetId (null for a root leaf
//     or string otherwise) because the TypeScript parser rejects a leaf whose
//     targetId field is missing (jsonl-storage.ts:103-105).
//   - active_tools_change entries must always carry activeToolNames as an array,
//     because TS always writes activeToolNames: [...names] (session.ts:169) and
//     the reader does [...entry.activeToolNames] (session.ts:36), which throws on
//     an undefined (omitted) field. SetActiveTools(ctx, nil) is a reachable path
//     that produces an empty slice that omitempty would otherwise drop.
//
// In both cases the field is appended before the closing brace, preserving valid
// JSON and the existing field order.
func (r entryRecord) MarshalJSON() ([]byte, error) {
	type alias entryRecord
	// Use the no-HTML-escape marshaller so <, >, and & survive in message
	// content. json.Encoder passes through (does not un-escape) bytes returned by
	// a MarshalJSON method, so escaping must be disabled here at the source rather
	// than relying on the outer marshalJSONLine encoder.
	data, err := marshalNoHTMLEscape(alias(r))
	if err != nil {
		return nil, err
	}
	if r.Type == "leaf" && r.TargetID == nil && len(data) >= 2 && data[len(data)-1] == '}' {
		return append(data[:len(data)-1], []byte(`,"targetId":null}`)...), nil
	}
	if r.Type == "active_tools_change" && len(r.ActiveToolNames) == 0 && len(data) >= 2 && data[len(data)-1] == '}' {
		return append(data[:len(data)-1], []byte(`,"activeToolNames":[]}`)...), nil
	}
	return data, nil
}

func (r *entryRecord) UnmarshalJSON(data []byte) error {
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
	type alias entryRecord
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
	out.Raw = append(json.RawMessage(nil), data...)
	*r = entryRecord(out)
	return nil
}

func marshalEntry(entry Entry) entryRecord {
	record := entryRecord{
		Type:      entry.EntryType(),
		ID:        entry.EntryID(),
		ParentID:  entry.EntryParentID(),
		Timestamp: entry.EntryTimestamp(),
	}
	switch e := entry.(type) {
	case MessageEntry:
		record.Message = e.Message
	case ThinkingLevelChangeEntry:
		record.ThinkingLevel = e.ThinkingLevel
	case ModelChangeEntry:
		record.Provider = e.Provider
		record.ModelID = e.ModelID
	case ActiveToolsChangeEntry:
		record.ActiveToolNames = append([]string(nil), e.ActiveToolNames...)
	case CompactionEntry:
		record.Summary = e.Summary
		record.FirstKeptEntryID = e.FirstKeptEntryID
		record.TokensBefore = e.TokensBefore
		record.Details = e.Details
		record.FromHook = e.FromHook
	case BranchSummaryEntry:
		record.FromID = e.FromID
		record.Summary = e.Summary
		record.Details = e.Details
		record.FromHook = e.FromHook
	case CustomEntry:
		record.CustomType = e.CustomType
		record.Data = e.Data
	case CustomMessageEntry:
		record.CustomType = e.CustomType
		record.Content = e.Content
		record.Display = e.Display
		record.Details = e.Details
	case LabelEntry:
		record.TargetID = &e.TargetID
		record.Label = e.Label
	case SessionInfoEntry:
		record.Name = e.Name
	case LeafEntry:
		record.TargetID = cloneStringPtr(e.TargetID)
	}
	return record
}

func unmarshalEntry(raw []byte) (Entry, error) {
	var record entryRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	base := BaseEntry{ID: record.ID, ParentID: cloneStringPtr(record.ParentID), Timestamp: record.Timestamp}
	switch record.Type {
	case "message":
		return MessageEntry{BaseEntry: base, Message: record.Message}, nil
	case "thinking_level_change":
		return ThinkingLevelChangeEntry{BaseEntry: base, ThinkingLevel: record.ThinkingLevel}, nil
	case "model_change":
		return ModelChangeEntry{BaseEntry: base, Provider: record.Provider, ModelID: record.ModelID}, nil
	case "active_tools_change":
		return ActiveToolsChangeEntry{BaseEntry: base, ActiveToolNames: append([]string(nil), record.ActiveToolNames...)}, nil
	case "compaction":
		return CompactionEntry{BaseEntry: base, Summary: record.Summary, FirstKeptEntryID: record.FirstKeptEntryID, TokensBefore: record.TokensBefore, Details: record.Details, FromHook: record.FromHook}, nil
	case "branch_summary":
		return BranchSummaryEntry{BaseEntry: base, FromID: record.FromID, Summary: record.Summary, Details: record.Details, FromHook: record.FromHook}, nil
	case "custom":
		return CustomEntry{BaseEntry: base, CustomType: record.CustomType, Data: record.Data}, nil
	case "custom_message":
		return CustomMessageEntry{BaseEntry: base, CustomType: record.CustomType, Content: record.Content, Display: record.Display, Details: record.Details}, nil
	case "label":
		targetID := ""
		if record.TargetID != nil {
			targetID = *record.TargetID
		}
		return LabelEntry{BaseEntry: base, TargetID: targetID, Label: record.Label}, nil
	case "session_info":
		return SessionInfoEntry{BaseEntry: base, Name: record.Name}, nil
	case "leaf":
		return LeafEntry{BaseEntry: base, TargetID: cloneStringPtr(record.TargetID)}, nil
	default:
		return nil, &SessionError{Code: "invalid_entry", Msg: "unknown entry type: " + record.Type}
	}
}
