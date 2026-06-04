package session

import (
	"bytes"
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

// MarshalJSON serializes an entry record. Several shared, omitempty fields must
// be re-added for specific entry types because the TypeScript writer always
// emits them and its parser/reader require them present even when empty/zero:
//   - Leaf entries must always carry an explicit targetId (null for a root leaf
//     or string otherwise) because the TypeScript parser rejects a leaf whose
//     targetId field is missing (jsonl-storage.ts:103-105).
//   - active_tools_change entries must always carry activeToolNames as an array,
//     because TS always writes activeToolNames: [...names] (session.ts:169) and
//     the reader does [...entry.activeToolNames] (session.ts:36), which throws on
//     an undefined (omitted) field. SetActiveTools(ctx, nil) is a reachable path
//     that produces an empty slice that omitempty would otherwise drop.
//   - compaction entries must always carry tokensBefore (number) and fromHook
//     (boolean): TS appendCompaction always writes both (session.ts:173-191), so
//     tokensBefore:0 and fromHook:false must survive Go's omitempty.
//   - branch_summary entries must always carry fromHook (boolean): TS moveTo
//     always writes it (session.ts:255-264).
//   - custom_message entries must always carry display (boolean): TS
//     appendCustomMessageEntry always writes it (session.ts:204-219).
//
// The re-added fields are inserted at the TS object-literal position (which the
// shared struct field order already reflects when the field is present):
//   - compaction: {summary, firstKeptEntryId, tokensBefore, details?, fromHook}
//   - custom_message: {customType, content, display, details?}
//   - branch_summary: {fromId, summary, details?, fromHook}
//
// tokensBefore is inserted right after firstKeptEntryId (i.e. before an optional
// details), display right after content, and the trailing fromHook is appended
// before the closing brace. details is still dropped when nil (TS drops
// undefined), matching the omitted-field behavior.
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
	switch r.Type {
	case "leaf":
		if r.TargetID == nil {
			data = appendBeforeClose(data, `"targetId":null`)
		}
	case "active_tools_change":
		if len(r.ActiveToolNames) == 0 {
			data = appendBeforeClose(data, `"activeToolNames":[]`)
		}
	case "compaction":
		// tokensBefore goes after firstKeptEntryId (before an optional details);
		// fromHook is the final field.
		if r.TokensBefore == 0 {
			data = insertAfterKey(data, "firstKeptEntryId", `"tokensBefore":0`)
		}
		if !r.FromHook {
			data = appendBeforeClose(data, `"fromHook":false`)
		}
	case "branch_summary":
		if !r.FromHook {
			data = appendBeforeClose(data, `"fromHook":false`)
		}
	case "custom_message":
		if !r.Display {
			data = insertAfterKey(data, "content", `"display":false`)
		}
	}
	return data, nil
}

// appendBeforeClose inserts field (a `"key":value` fragment) immediately before
// the trailing '}' of a JSON object, preserving valid JSON. The object is never
// empty here (it always carries type/id/parentId/timestamp), so a leading comma
// is always correct.
func appendBeforeClose(data []byte, field string) []byte {
	if len(data) < 2 || data[len(data)-1] != '}' {
		return data
	}
	out := make([]byte, 0, len(data)+len(field)+1)
	out = append(out, data[:len(data)-1]...)
	out = append(out, ',')
	out = append(out, field...)
	out = append(out, '}')
	return out
}

// insertAfterKey inserts field (a `"key":value` fragment) immediately after the
// value of an existing object key, preserving TS field order when a later
// optional field (e.g. details) is present. If the anchor key is absent the
// fragment is appended before the closing brace as a fallback.
func insertAfterKey(data []byte, key, field string) []byte {
	marker := []byte(`"` + key + `":`)
	idx := bytes.Index(data, marker)
	if idx < 0 {
		return appendBeforeClose(data, field)
	}
	// Walk past the key's value to the comma or closing brace that follows it.
	i := idx + len(marker)
	end := scanJSONValueEnd(data, i)
	if end < 0 {
		return appendBeforeClose(data, field)
	}
	out := make([]byte, 0, len(data)+len(field)+1)
	out = append(out, data[:end]...)
	out = append(out, ',')
	out = append(out, field...)
	out = append(out, data[end:]...)
	return out
}

// scanJSONValueEnd returns the index just past the JSON value starting at start,
// i.e. the index of the ',' or '}' that terminates it. It handles strings
// (with escapes) and other scalars/containers by tracking quoting and nesting
// depth; objects/arrays are skipped wholesale. Returns -1 if malformed.
func scanJSONValueEnd(data []byte, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(data); i++ {
		c := data[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				// Closing brace of the enclosing object (value ended).
				return i
			}
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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
