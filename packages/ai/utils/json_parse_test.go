package utils

import (
	"reflect"
	"testing"
)

func TestParseStreamingJSONComplete(t *testing.T) {
	got := ParseStreamingJSON(`{"command":"ls -la","cwd":"/tmp"}`)
	want := map[string]any{"command": "ls -la", "cwd": "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseStreamingJSONEmpty(t *testing.T) {
	if got := ParseStreamingJSON(""); len(got) != 0 {
		t.Fatalf("got %#v, want empty", got)
	}
	if got := ParseStreamingJSON("   "); len(got) != 0 {
		t.Fatalf("got %#v, want empty", got)
	}
}

func TestParseStreamingJSONRecoversPartialValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]any
	}{
		{
			// Truncated mid string value: the partial value must be preserved,
			// not dropped (this is the production "tool args truncated" case).
			name:  "truncated_mid_string_value",
			input: `{"path":"/foo/ba`,
			want:  map[string]any{"path": "/foo/ba"},
		},
		{
			name:  "truncated_after_value_before_brace",
			input: `{"a":1,"b":2`,
			want:  map[string]any{"a": float64(1), "b": float64(2)},
		},
		{
			name:  "trailing_comma",
			input: `{"a":1,`,
			want:  map[string]any{"a": float64(1)},
		},
		{
			name:  "dangling_colon_drops_incomplete_key",
			input: `{"a":1,"b":`,
			want:  map[string]any{"a": float64(1)},
		},
		{
			name:  "incomplete_trailing_key_dropped",
			input: `{"a":1,"bcd`,
			want:  map[string]any{"a": float64(1)},
		},
		{
			name:  "nested_object_truncated",
			input: `{"outer":{"inner":"val`,
			want:  map[string]any{"outer": map[string]any{"inner": "val"}},
		},
		{
			name:  "nested_object_complete_value_then_truncate",
			input: `{"a":{"b":1`,
			want:  map[string]any{"a": map[string]any{"b": float64(1)}},
		},
		{
			name:  "array_value_truncated",
			input: `{"items":[1,2,`,
			want:  map[string]any{"items": []any{float64(1), float64(2)}},
		},
		{
			name:  "partial_literal_dropped",
			input: `{"a":1,"flag":tru`,
			want:  map[string]any{"a": float64(1)},
		},
		{
			name:  "string_with_escaped_quote_truncated",
			input: `{"msg":"he said \"hi`,
			want:  map[string]any{"msg": `he said "hi`},
		},
		{
			name:  "open_object_only",
			input: `{`,
			want:  map[string]any{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseStreamingJSON(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseStreamingJSON(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseStreamingJSONRepairsControlCharacters(t *testing.T) {
	// A raw newline inside a string is invalid JSON; repair should escape it.
	got := ParseStreamingJSON("{\"text\":\"line1\nline2\"}")
	want := map[string]any{"text": "line1\nline2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
