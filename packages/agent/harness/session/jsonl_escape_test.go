package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

// escLT, escGT, escAmp, escLS, and escPS are the JSON \u escape sequences that Go's
// default json.Marshal emits but TS JSON.stringify does not (for < > &) / does
// (for U+2028). They are built from runes so the test source carries no literal
// special characters and no ambiguity about which bytes are present.
var (
	escLT  = `\u00` + "3c" // < -> <
	escGT  = `\u00` + "3e" // > -> >
	escAmp = `\u00` + "26" // & -> &
	escLS  = `\u20` + "28" // U+2028 ->
	escPS  = `\u20` + "29" // U+2029 ->
)

// TestSessionJSONLWriteGoldenNoHTMLEscape locks the byte-for-byte shape of the
// write direction: Go-written session JSONL must match what TS JSON.stringify
// produces (jsonl-storage.ts:209/238/252 use `${JSON.stringify(value)}\n`).
//
// The critical property is that <, >, and & appear literally rather than as
// < / > / &. Go's stdlib json.Marshal HTML-escapes those runes;
// TS JSON.stringify never does. The expected strings below were produced by
// Node's JSON.stringify on the equivalent objects, so they are the actual TS
// wire bytes.
func TestSessionJSONLWriteGoldenNoHTMLEscape(t *testing.T) {
	tests := []struct {
		name  string
		entry Entry
		want  string
	}{
		{
			name: "header",
			// Header goes through marshalJSONLine directly (not entryRecord).
		},
		{
			name: "user message with angle brackets and ampersand",
			entry: MessageEntry{
				BaseEntry: BaseEntry{ID: "id1", Timestamp: "2026-06-02T03:52:13.836Z"},
				Message: ai.UserMessage{
					Role:        "user",
					Content:     ai.TextBlocks("a < b && c > d <file>x</file>"),
					TimestampMs: 1780372333836,
				},
			},
			want: `{"type":"message","id":"id1","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","message":{"role":"user","content":[{"type":"text","text":"a < b && c > d <file>x</file>"}],"timestamp":1780372333836}}`,
		},
		{
			name: "compaction summary with html",
			entry: CompactionEntry{
				BaseEntry:        BaseEntry{ID: "c1", Timestamp: "2026-06-02T03:52:13.836Z"},
				Summary:          "use <div> & List<String>",
				FirstKeptEntryID: "k1",
				TokensBefore:     42,
			},
			want: `{"type":"compaction","id":"c1","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","summary":"use <div> & List<String>","firstKeptEntryId":"k1","tokensBefore":42,"fromHook":false}`,
		},
		{
			name: "branch summary with html",
			entry: BranchSummaryEntry{
				BaseEntry: BaseEntry{ID: "b1", Timestamp: "2026-06-02T03:52:13.836Z"},
				FromID:    "root",
				Summary:   "a & b < c > d",
			},
			want: `{"type":"branch_summary","id":"b1","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","fromId":"root","summary":"a & b < c > d","fromHook":false}`,
		},
		{
			name: "custom_message with string content containing html",
			entry: CustomMessageEntry{
				BaseEntry:  BaseEntry{ID: "cm1", Timestamp: "2026-06-02T03:52:13.836Z"},
				CustomType: "note",
				Content:    "<b>bold</b> & co",
				Display:    true,
			},
			want: `{"type":"custom_message","id":"cm1","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","customType":"note","content":"<b>bold</b> & co","display":true}`,
		},
		{
			// TS appendCompaction always writes tokensBefore and fromHook, even at
			// zero/false (session.ts:173-191). Go's omitempty would drop both; the
			// MarshalJSON re-adds them at the TS positions {…,tokensBefore,fromHook}.
			name: "compaction zero tokensBefore and fromHook",
			entry: CompactionEntry{
				BaseEntry:        BaseEntry{ID: "c2", Timestamp: "2026-06-02T03:52:13.836Z"},
				Summary:          "s",
				FirstKeptEntryID: "k2",
				TokensBefore:     0,
				FromHook:         false,
			},
			want: `{"type":"compaction","id":"c2","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","summary":"s","firstKeptEntryId":"k2","tokensBefore":0,"fromHook":false}`,
		},
		{
			// details (when present) must sit between tokensBefore and fromHook,
			// matching the TS object-literal order {summary, firstKeptEntryId,
			// tokensBefore, details, fromHook}. Verifies tokensBefore is inserted
			// before details (not appended at the end) when tokensBefore is zero.
			name: "compaction zero tokensBefore with details preserves order",
			entry: CompactionEntry{
				BaseEntry:        BaseEntry{ID: "c3", Timestamp: "2026-06-02T03:52:13.836Z"},
				Summary:          "s",
				FirstKeptEntryID: "k3",
				TokensBefore:     0,
				Details:          map[string]any{"reason": "auto"},
				FromHook:         true,
			},
			want: `{"type":"compaction","id":"c3","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","summary":"s","firstKeptEntryId":"k3","tokensBefore":0,"details":{"reason":"auto"},"fromHook":true}`,
		},
		{
			// TS moveTo always writes fromHook on branch_summary (session.ts:255-264).
			name: "branch_summary fromHook false",
			entry: BranchSummaryEntry{
				BaseEntry: BaseEntry{ID: "b2", Timestamp: "2026-06-02T03:52:13.836Z"},
				FromID:    "root",
				Summary:   "s",
				FromHook:  false,
			},
			want: `{"type":"branch_summary","id":"b2","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","fromId":"root","summary":"s","fromHook":false}`,
		},
		{
			// TS appendCustomMessageEntry always writes display (session.ts:204-219);
			// display:false must survive. With details present, display sits between
			// content and details: {customType, content, display, details}.
			name: "custom_message display false with details",
			entry: CustomMessageEntry{
				BaseEntry:  BaseEntry{ID: "cm2", Timestamp: "2026-06-02T03:52:13.836Z"},
				CustomType: "note",
				Content:    "hi",
				Display:    false,
				Details:    map[string]any{"k": "v"},
			},
			want: `{"type":"custom_message","id":"cm2","parentId":null,"timestamp":"2026-06-02T03:52:13.836Z","customType":"note","content":"hi","display":false,"details":{"k":"v"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "header" {
				header := JSONLHeader{
					Type:      "session",
					Version:   3,
					ID:        "s<1>&",
					Timestamp: "2026-06-02T03:52:13.836Z",
					Cwd:       "/work<&>",
				}
				line, err := marshalJSONLine(header)
				if err != nil {
					t.Fatal(err)
				}
				want := `{"type":"session","version":3,"id":"s<1>&","timestamp":"2026-06-02T03:52:13.836Z","cwd":"/work<&>"}` + "\n"
				if string(line) != want {
					t.Fatalf("header golden mismatch:\n got: %q\nwant: %q", line, want)
				}
				return
			}

			line, err := marshalJSONLine(marshalEntry(tc.entry))
			if err != nil {
				t.Fatal(err)
			}
			got := string(line)
			if got != tc.want+"\n" {
				t.Fatalf("golden mismatch:\n got: %q\nwant: %q", got, tc.want+"\n")
			}
			// Explicit guard: none of <, >, & may appear in their \u-escaped form.
			for _, esc := range []string{escLT, escGT, escAmp} {
				if strings.Contains(got, esc) {
					t.Fatalf("output must not contain escaped sequence %s: %s", esc, got)
				}
			}
		})
	}
}

// TestSessionJSONLRoundTripSpecialChars verifies that entries whose content
// contains <, >, and & write to literal (unescaped) bytes and parse back to the
// exact same values. This guards the full write->read cycle for the characters
// that the HTML-escape divergence affects.
func TestSessionJSONLRoundTripSpecialChars(t *testing.T) {
	// No embedded double-quotes so the exact bytes can be asserted as a literal
	// substring (a " would legitimately JSON-escape to \", which is orthogonal to
	// the < > & HTML-escape divergence under test). Round-trip equality below
	// still covers all escaping.
	const payload = "if (a < b && c > d) { return <Foo bar=baz & qux />; }"

	entries := []Entry{
		MessageEntry{
			BaseEntry: BaseEntry{ID: "m1", Timestamp: "2026-06-02T03:52:13.836Z"},
			Message: ai.UserMessage{
				Role:        "user",
				Content:     ai.TextBlocks(payload),
				TimestampMs: 1,
			},
		},
		CustomMessageEntry{
			BaseEntry:  BaseEntry{ID: "cm1", Timestamp: "2026-06-02T03:52:13.836Z"},
			CustomType: "note",
			Content:    payload,
		},
		CompactionEntry{
			BaseEntry: BaseEntry{ID: "c1", Timestamp: "2026-06-02T03:52:13.836Z"},
			Summary:   payload,
		},
	}

	for _, entry := range entries {
		line, err := marshalJSONLine(marshalEntry(entry))
		if err != nil {
			t.Fatalf("%T marshal: %v", entry, err)
		}
		// The literal characters must be present unescaped in the bytes.
		if !strings.Contains(string(line), payload) {
			t.Fatalf("%T: payload not present literally in %s", entry, line)
		}

		back, err := unmarshalEntry(line)
		if err != nil {
			t.Fatalf("%T re-parse: %v", entry, err)
		}
		switch entry.(type) {
		case MessageEntry:
			got, ok := back.(MessageEntry)
			if !ok {
				t.Fatalf("expected MessageEntry, got %T", back)
			}
			if text := ai.MessageText(got.Message); text != payload {
				t.Fatalf("message text round-trip mismatch:\n got: %q\nwant: %q", text, payload)
			}
		case CustomMessageEntry:
			got, ok := back.(CustomMessageEntry)
			if !ok {
				t.Fatalf("expected CustomMessageEntry, got %T", back)
			}
			if got.Content != payload {
				t.Fatalf("custom_message content round-trip mismatch:\n got: %v\nwant: %q", got.Content, payload)
			}
		case CompactionEntry:
			got, ok := back.(CompactionEntry)
			if !ok {
				t.Fatalf("expected CompactionEntry, got %T", back)
			}
			if got.Summary != payload {
				t.Fatalf("compaction summary round-trip mismatch:\n got: %q\nwant: %q", got.Summary, payload)
			}
		}
	}
}

// TestMarshalNoHTMLEscapeMatchesTSStringify locks the helper's behavior against
// JSON.stringify for the affected characters.
func TestMarshalNoHTMLEscapeMatchesTSStringify(t *testing.T) {
	// <, >, & are left literal (matches JSON.stringify exactly).
	got, err := marshalNoHTMLEscape(map[string]string{"v": "a<b>c&d"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":"a<b>c&d"}`; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(string(got), escLT) || strings.Contains(string(got), escAmp) {
		t.Fatalf("must not HTML-escape: %s", got)
	}

	// No trailing newline (drop-in replacement for json.Marshal).
	if strings.HasSuffix(string(got), "\n") {
		t.Fatalf("marshalNoHTMLEscape must not append a trailing newline: %q", got)
	}

	input := "line" + string(rune(0x2028)) + "break" + string(rune(0x2029)) + ` literal \u2028`
	sep, err := marshalNoHTMLEscape(map[string]string{"v": input})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sep), `"v":"line`+escLS) || strings.Contains(string(sep), "break"+escPS) {
		t.Fatalf("must not escape real U+2028/U+2029: %s", sep)
	}
	if !strings.Contains(string(sep), string(rune(0x2028))) || !strings.Contains(string(sep), string(rune(0x2029))) {
		t.Fatalf("expected literal U+2028/U+2029, got %s", sep)
	}
	if !strings.Contains(string(sep), `\\u2028`) {
		t.Fatalf("literal backslash-u text should stay escaped as JSON text, got %s", sep)
	}
	var decoded map[string]string
	if err := json.Unmarshal(sep, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["v"] != input {
		t.Fatalf("U+2028/U+2029 must decode back to the same character, got %q", decoded["v"])
	}
}
