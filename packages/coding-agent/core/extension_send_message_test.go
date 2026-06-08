package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
)

func TestAgentSessionExtensionSendMessageAppendsCustomMessage(t *testing.T) {
	session := InMemorySession(t.TempDir())
	agent := &AgentSession{Session: session}
	raw, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name: "sendMessage",
		Params: json.RawMessage(`{
			"message": {
				"customType": "status-update",
				"content": "from extension",
				"display": false,
				"details": { "level": "warn" }
			},
			"options": { "triggerTurn": true }
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		OK                   bool   `json:"ok"`
		EntryID              string `json:"entryId"`
		TriggerTurnRequested bool   `json:"triggerTurnRequested"`
		TriggerTurnHandled   bool   `json:"triggerTurnHandled"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v (%s)", err, raw)
	}
	if !result.OK || result.EntryID == "" || !result.TriggerTurnRequested || result.TriggerTurnHandled {
		t.Fatalf("result=%#v", result)
	}
	if len(session.Entries) != 1 {
		t.Fatalf("entries=%#v", session.Entries)
	}
	entry := session.Entries[0]
	if entry.Type != "custom_message" || entry.ID != result.EntryID || entry.CustomType != "status-update" {
		t.Fatalf("entry=%#v result=%#v", entry, result)
	}
	if entry.Content != "from extension" || entry.Display {
		t.Fatalf("entry content/display=%#v", entry)
	}
	details, ok := entry.Details.(map[string]any)
	if !ok || details["level"] != "warn" {
		t.Fatalf("entry details=%#v", entry.Details)
	}
}

func TestAgentSessionExtensionSendMessageTriggerTurnHandler(t *testing.T) {
	session := InMemorySession(t.TempDir())
	agent := &AgentSession{Session: session}
	called := 0
	agent.SetExtensionTriggerTurnHandler(func(ctx context.Context) error {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		called++
		return nil
	})
	raw, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name:   "sendMessage",
		Params: json.RawMessage(`{"message":{"customType":"status-update","content":"from extension"},"options":{"triggerTurn":true}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("trigger handler called %d times", called)
	}
	var result struct {
		TriggerTurnRequested bool `json:"triggerTurnRequested"`
		TriggerTurnHandled   bool `json:"triggerTurnHandled"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v (%s)", err, raw)
	}
	if !result.TriggerTurnRequested || !result.TriggerTurnHandled {
		t.Fatalf("result=%#v", result)
	}
}

func TestAgentSessionExtensionSendUserMessageUsesHandler(t *testing.T) {
	session := InMemorySession(t.TempDir())
	agent := &AgentSession{Session: session}
	var got SendUserMessageOptions
	called := 0
	agent.SetExtensionUserMessageHandler(func(ctx context.Context, opts SendUserMessageOptions) error {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		called++
		got = opts
		return nil
	})
	raw, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name: "sendUserMessage",
		Params: json.RawMessage(`{
			"content": [
				{ "type": "text", "text": "first" },
				{ "type": "text", "text": "second" },
				{ "type": "image", "mimeType": "image/png", "data": "abc" }
			],
			"options": { "deliverAs": "followUp" }
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("handler called %d times", called)
	}
	if got.Text != "first\nsecond" || got.StreamingBehavior != StreamingFollowUp || got.Source != InputExtension {
		t.Fatalf("options=%#v", got)
	}
	if len(got.Images) != 1 || got.Images[0].Type != "image" || got.Images[0].MimeType != "image/png" || got.Images[0].Data != "abc" {
		t.Fatalf("images=%#v", got.Images)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v (%s)", err, raw)
	}
	if !result.OK {
		t.Fatalf("result=%#v", result)
	}
}

func TestAgentSessionExtensionAppendEntrySessionNameAndLabel(t *testing.T) {
	session := InMemorySession(t.TempDir())
	if err := session.AppendMessage(ai.NewUserMessage("hello", nil)); err != nil {
		t.Fatal(err)
	}
	targetID := session.Entries[0].ID
	agent := &AgentSession{Session: session}

	raw, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name:   "appendEntry",
		Params: json.RawMessage(`{"customType":"state","data":{"value":1}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var appendResult struct {
		OK      bool   `json:"ok"`
		EntryID string `json:"entryId"`
	}
	if err := json.Unmarshal(raw, &appendResult); err != nil {
		t.Fatalf("decode append result: %v (%s)", err, raw)
	}
	if !appendResult.OK || appendResult.EntryID == "" {
		t.Fatalf("append result=%#v", appendResult)
	}
	if got := session.Entries[1]; got.Type != "custom" || got.CustomType != "state" || string(got.Data) != `{"value":1}` {
		t.Fatalf("custom entry=%#v data=%s", got, got.Data)
	}

	if _, err := agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name:   "setSessionName",
		Params: json.RawMessage(`{"name":"  Demo Session  "}`),
	}); err != nil {
		t.Fatal(err)
	}
	raw, err = agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{Name: "getSessionName"})
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		t.Fatalf("decode session name: %v (%s)", err, raw)
	}
	if name != "Demo Session" {
		t.Fatalf("name=%q", name)
	}

	raw, err = agent.handleExtensionContextAction(context.Background(), coreext.ExtensionContextAction{
		Name:   "setLabel",
		Params: json.RawMessage(`{"entryId":"` + targetID + `","label":"important"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var labelResult struct {
		OK      bool   `json:"ok"`
		EntryID string `json:"entryId"`
	}
	if err := json.Unmarshal(raw, &labelResult); err != nil {
		t.Fatalf("decode label result: %v (%s)", err, raw)
	}
	if !labelResult.OK || labelResult.EntryID == "" {
		t.Fatalf("label result=%#v", labelResult)
	}
	last := session.Entries[len(session.Entries)-1]
	if last.Type != "label" || last.TargetID != targetID || last.Label != "important" {
		t.Fatalf("label entry=%#v target=%q", last, targetID)
	}
}
