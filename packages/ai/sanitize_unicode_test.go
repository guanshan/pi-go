package ai

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeUnicodeRemovesUnpairedSurrogateBytes(t *testing.T) {
	unpairedHighSurrogate := string([]byte{0xed, 0xa0, 0xbd})
	got := SanitizeUnicode("before " + unpairedHighSurrogate + " after")
	if got != "before  after" {
		t.Fatalf("unexpected sanitized text: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("sanitized text should be valid UTF-8")
	}
}

func TestSanitizeUnicodePreservesEmojiAndCJK(t *testing.T) {
	text := "emoji 🙈 rocket 🚀 Chinese 你好 Japanese こんにちは"
	if got := SanitizeUnicode(text); got != text {
		t.Fatalf("valid Unicode should be preserved: %q", got)
	}
}

func TestMessageConstructorsSanitizeStringContent(t *testing.T) {
	invalid := string([]byte{0xed, 0xa0, 0xbd})
	user := NewUserMessage("hello "+invalid+"world", nil)
	if got := MessageText(user); got != "hello world" {
		t.Fatalf("unexpected user text: %q", got)
	}

	assistant := NewAssistantMessage("test-api", "test", "model", []ContentBlock{{Type: "text", Text: "ok " + invalid}}, Usage{}, "stop")
	if got := MessageText(assistant); got != "ok " {
		t.Fatalf("unexpected assistant text: %q", got)
	}

	tool := NewToolResultMessage("call", "tool", []ContentBlock{{Type: "text", Text: "think " + invalid}}, nil, false)
	if got := MessageText(tool); got != "think " {
		t.Fatalf("unexpected tool result text: %q", got)
	}
}

func TestChatRequestSanitizationAppliesAtEntry(t *testing.T) {
	invalid := string([]byte{0xed, 0xa0, 0xbd})
	req, err := prepareChatRequest(ChatRequest{
		Model:        Model{Provider: "faux", ID: "faux", API: "faux"},
		SystemPrompt: "system " + invalid,
		Messages: []Message{
			UserMessage{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi " + invalid}}},
			AssistantMessage{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "ok " + invalid}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(req.SystemPrompt, invalid) {
		t.Fatal("system prompt was not sanitized")
	}
	if got := MessageText(req.Messages[0]); got != "hi " {
		t.Fatalf("unexpected request message text: %q", got)
	}
	if got := MessageText(req.Messages[1]); got != "ok " {
		t.Fatalf("unexpected request assistant text: %q", got)
	}
}
