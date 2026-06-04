package core

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestWriteJSONLineNoHTMLEscape asserts that session JSONL serialization does
// not HTML-escape `<`, `>`, `&` (matching TS JSON.stringify) and preserves
// emoji and U+2028/U+2029 verbatim, just like writeRPCJSONLine on the RPC path.
func TestWriteJSONLineNoHTMLEscape(t *testing.T) {
	seps := "line" + string(rune(0x2028)) + "para" + string(rune(0x2029)) + "end"
	payload := map[string]string{
		"code":    "if (a < b && c > d) { x(); }",
		"amp":     "Tom & Jerry",
		"emoji":   "rocket \U0001F680 ok",
		"literal": `literal \u2028 text`,
		"seps":    seps,
	}
	var buf bytes.Buffer
	if err := writeJSONLine(&buf, payload); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}
	got := buf.String()

	// `<`, `>`, `&`, emoji, and the JS line/paragraph separators are left literal
	// (matches JSON.stringify exactly).
	for _, want := range []string{
		`"if (a < b && c > d) { x(); }"`,
		`"Tom & Jerry"`,
		"rocket \U0001F680 ok",
		seps,
		`"literal":"literal \\u2028 text"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output: %q", want, got)
		}
	}
	for _, escaped := range []string{"\\u003c", "\\u003e", "\\u0026", "\\u2029"} {
		if strings.Contains(got, escaped) {
			t.Errorf("output unexpectedly escaped (%s)\nfull output: %q", escaped, got)
		}
	}
	if strings.Contains(got, `"seps":"line\u2028`) {
		t.Errorf("real U+2028 was escaped instead of left literal\nfull output: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output not newline-terminated: %q", got)
	}
}

func TestCoreSessionEntryWritesTypeScopedDefaultFields(t *testing.T) {
	parent := "root"
	tests := []struct {
		name  string
		entry SessionEntry
		want  string
	}{
		{
			name: "compaction zero tokens and fromHook false",
			entry: SessionEntry{
				Type:         "compaction",
				ID:           "c1",
				ParentID:     &parent,
				Timestamp:    "2026-06-02T03:52:13.836Z",
				Summary:      "use <div> & List<String>",
				FirstKeptID:  "k1",
				TokensBefore: 0,
				Details:      map[string]any{"reason": "auto"},
				FromHook:     false,
				FromHookSet:  true,
			},
			want: `{"type":"compaction","id":"c1","parentId":"root","timestamp":"2026-06-02T03:52:13.836Z","summary":"use <div> & List<String>","firstKeptEntryId":"k1","tokensBefore":0,"details":{"reason":"auto"},"fromHook":false}` + "\n",
		},
		{
			name: "branch summary fromHook false",
			entry: SessionEntry{
				Type:        "branch_summary",
				ID:          "b1",
				ParentID:    &parent,
				Timestamp:   "2026-06-02T03:52:13.836Z",
				FromID:      "old",
				Summary:     "a & b < c > d",
				FromHook:    false,
				FromHookSet: true,
			},
			want: `{"type":"branch_summary","id":"b1","parentId":"root","timestamp":"2026-06-02T03:52:13.836Z","fromId":"old","summary":"a & b < c > d","fromHook":false}` + "\n",
		},
		{
			name: "custom message display false",
			entry: SessionEntry{
				Type:       "custom_message",
				ID:         "m1",
				ParentID:   &parent,
				Timestamp:  "2026-06-02T03:52:13.836Z",
				CustomType: "note",
				Content:    "hidden <b>&</b>",
				Display:    false,
				Details:    map[string]any{"k": "v"},
			},
			want: `{"type":"custom_message","id":"m1","parentId":"root","timestamp":"2026-06-02T03:52:13.836Z","customType":"note","content":"hidden <b>&</b>","display":false,"details":{"k":"v"}}` + "\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeJSONLine(&buf, tc.entry); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("jsonl mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestCoreSessionRewritePreservesDefaultFields(t *testing.T) {
	session := InMemorySession("/tmp/rewrite")
	parent := "root"
	session.InMemory = false
	session.Path = t.TempDir() + "/session.jsonl"
	session.CurrentID = &parent
	session.Entries = []SessionEntry{
		{Type: "compaction", ID: "c1", ParentID: &parent, Timestamp: "2026-06-02T03:52:13.836Z", Summary: "s", FirstKeptID: "root", TokensBefore: 0, FromHook: false, FromHookSet: true},
		{Type: "custom_message", ID: "m1", ParentID: &parent, Timestamp: "2026-06-02T03:52:14.836Z", CustomType: "note", Content: "hi", Display: false},
	}
	if _, err := session.rewrite(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(session.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"tokensBefore":0`, `"fromHook":false`, `"display":false`} {
		if !strings.Contains(text, want) {
			t.Fatalf("rewritten session missing %s:\n%s", want, text)
		}
	}
}

func TestCoreSessionRewritePreservesMissingFromHook(t *testing.T) {
	session := InMemorySession("/tmp/rewrite")
	parent := "root"
	session.InMemory = false
	session.Path = t.TempDir() + "/session.jsonl"
	session.CurrentID = &parent
	session.Entries = []SessionEntry{
		{Type: "compaction", ID: "c1", ParentID: &parent, Timestamp: "2026-06-02T03:52:13.836Z", Summary: "s", FirstKeptID: "root", TokensBefore: 10},
		{Type: "branch_summary", ID: "b1", ParentID: &parent, Timestamp: "2026-06-02T03:52:14.836Z", FromID: "old", Summary: "branch"},
	}
	if _, err := session.rewrite(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(session.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"fromHook"`) {
		t.Fatalf("rewrite should preserve legacy missing fromHook fields:\n%s", raw)
	}
}
