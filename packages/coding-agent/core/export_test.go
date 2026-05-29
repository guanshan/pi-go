package core

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestExportSessionToHTMLRendersRichViewer(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	sm, err := NewSessionManager(cwd, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.AppendSessionName("Export test"); err != nil {
		t.Fatal(err)
	}
	user := ai.NewUserMessage("hello\n</script><script>alert(1)</script>", []ai.ContentBlock{{
		Type:     "image",
		Data:     "iVBORw0KGgo=",
		MimeType: "image/png",
	}})
	if err := sm.AppendMessage(user); err != nil {
		t.Fatal(err)
	}
	userID := sm.Entries[len(sm.Entries)-1].ID
	if err := sm.Append(SessionEntry{Type: "label", TargetID: userID, Label: "important"}); err != nil {
		t.Fatal(err)
	}
	assistant := ai.NewAssistantMessage("faux", "faux", "faux", []ai.ContentBlock{
		{Type: "thinking", Thinking: "consider <tag>"},
		{Type: "text", Text: "# Result\nok"},
		{Type: "toolCall", ID: "call-1", Name: "bash", Arguments: json.RawMessage(`{"command":"echo hi"}`)},
	}, ai.Usage{Input: 2, Output: 3, TotalTokens: 5, Cost: ai.Cost{Total: 0.125}}, "toolUse")
	if err := sm.AppendMessage(assistant); err != nil {
		t.Fatal(err)
	}
	tool := ai.NewToolResultMessage("call-1", "bash", []ai.ContentBlock{
		{Type: "text", Text: "output <ok>"},
		{Type: "image", Data: "R0lGODlhAQABAAAAACw=", MimeType: "image/gif"},
	}, map[string]any{"exitCode": 0}, false)
	if err := sm.AppendMessage(tool); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "session.html")
	got, err := ExportSessionToHTML(sm.File(), out)
	if err != nil {
		t.Fatal(err)
	}
	if got != out {
		t.Fatalf("output path = %q, want %q", got, out)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{
		`<!DOCTYPE html>`,
		`<meta name="pi-share-base-url"`,
		`id="session-data" type="application/json"`,
		`window.__PI_SESSION_DATA__`,
		`class="sidebar-search"`,
		`class="user-message"`,
		`class="assistant-message"`,
		`class="tool-execution success"`,
		`class="copy-link-btn"`,
		`class="message-images"`,
		`class="tool-images"`,
		`data:image/png;base64,iVBORw0KGgo=`,
		`data:image/gif;base64,R0lGODlhAQABAAAAACw=`,
		`&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;`,
		`output &lt;ok&gt;`,
		`important`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("export HTML missing %q", want)
		}
	}
	if strings.Contains(html, `</script><script>alert(1)</script>`) {
		t.Fatal("export HTML included unescaped script content")
	}

	encoded := extractBetween(t, html, `<script id="session-data" type="application/json">`, `</script>`)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var data exportSessionData
	if err := json.Unmarshal(decoded, &data); err != nil {
		t.Fatal(err)
	}
	if data.Header.ID != sm.Header.ID {
		t.Fatalf("embedded header id = %q, want %q", data.Header.ID, sm.Header.ID)
	}
	if data.LeafID == nil || *data.LeafID == "" {
		t.Fatalf("embedded leaf id missing: %#v", data.LeafID)
	}
	if data.Stats.UserMessages != 1 || data.Stats.AssistantMessages != 1 || data.Stats.ToolResults != 1 || data.Stats.ToolCalls != 1 {
		t.Fatalf("unexpected embedded stats: %#v", data.Stats)
	}
	if data.Version != Version {
		t.Fatalf("embedded version = %q, want %q", data.Version, Version)
	}
}

func TestDefaultExportPathMatchesTypeScriptCLI(t *testing.T) {
	got := defaultExportPath("/tmp/20260101T000000_abcd1234.jsonl")
	want := "pi-session-20260101T000000_abcd1234.html"
	if got != want {
		t.Fatalf("default export path = %q, want %q", got, want)
	}
}

func extractBetween(t *testing.T, text, start, end string) string {
	t.Helper()
	startIndex := strings.Index(text, start)
	if startIndex < 0 {
		t.Fatalf("missing start marker %q", start)
	}
	rest := text[startIndex+len(start):]
	endIndex := strings.Index(rest, end)
	if endIndex < 0 {
		t.Fatalf("missing end marker %q", end)
	}
	return rest[:endIndex]
}
