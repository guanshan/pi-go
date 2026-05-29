package ai

import (
	"encoding/json"
	"testing"

	aiproviders "github.com/guanshan/pi-go/packages/ai/providers"
)

func TestTransformMessagesDowngradesAndRepairsConversation(t *testing.T) {
	model := Model{Provider: "openai", ID: "gpt-text", API: "openai-completions", Input: []string{"text"}}
	assistant := NewAssistantMessage("other-api", "other", "foreign-model", []ContentBlock{
		{Type: "thinking", Thinking: "scratch", Signature: "foreign-sig"},
		{Type: "toolCall", ID: "call|1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`), ThoughtSignature: "thought"},
	}, Usage{}, "toolUse")
	messages := []Message{
		NewUserMessage("look", []ContentBlock{{Type: "image", MimeType: "image/png", Data: "abc"}}),
		assistant,
		NewUserMessage("next", nil),
	}

	got := transformMessages(messages, model, func(id string, _ Model, _ AssistantMessage) string {
		if id == "call|1" {
			return "call_1"
		}
		return id
	})

	user := got[0].(UserMessage)
	if len(user.Content) != 2 || user.Content[1].Text != nonVisionUserImagePlaceholder {
		t.Fatalf("user content=%#v", user.Content)
	}
	assistantOut := got[1].(AssistantMessage)
	if len(assistantOut.Content) != 2 || assistantOut.Content[0].Type != "text" || assistantOut.Content[0].Text != "scratch" {
		t.Fatalf("assistant content=%#v", assistantOut.Content)
	}
	if assistantOut.Content[1].ID != "call_1" || assistantOut.Content[1].ThoughtSignature != "" {
		t.Fatalf("tool call=%#v", assistantOut.Content[1])
	}
	repair := got[2].(ToolResultMessage)
	if repair.ToolCallID != "call_1" || !repair.IsError || MessageText(repair) != "No result provided" {
		t.Fatalf("repair=%#v", repair)
	}
}

func TestOpenAIResponsesThinkingUsesRawItem(t *testing.T) {
	raw := json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)
	block := openAIResponseBlock(aiproviders.OpenAIResponsesBlock{Type: "thinking", Thinking: "summary", RawItem: raw})
	if string(block.RawItem) != string(raw) || block.Signature != "" {
		t.Fatalf("block=%#v", block)
	}
}
