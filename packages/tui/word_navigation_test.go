package tui

import "testing"

func TestFindWordBackward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 11, 6},
		{"hello world", 6, 0},
		{"hello   world", 13, 8},
		{"a, b", 4, 3},
		{"", 0, 0},
		{"hi", 0, 0},
	}
	for _, c := range cases {
		got := FindWordBackward(c.text, c.cursor)
		if got != c.want {
			t.Errorf("FindWordBackward(%q, %d) = %d, want %d", c.text, c.cursor, got, c.want)
		}
	}
}

func TestFindWordForward(t *testing.T) {
	cases := []struct {
		text   string
		cursor int
		want   int
	}{
		{"hello world", 0, 5},
		{"hello world", 5, 11},
		{"hello   world", 5, 13},
		{"a, b", 0, 1},
		{"a, b", 1, 2},
		{"hi", 2, 2},
	}
	for _, c := range cases {
		got := FindWordForward(c.text, c.cursor)
		if got != c.want {
			t.Errorf("FindWordForward(%q, %d) = %d, want %d", c.text, c.cursor, got, c.want)
		}
	}
}
