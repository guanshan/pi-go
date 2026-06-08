package agent

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestUUIDv7Shape(t *testing.T) {
	id := UUIDv7()
	matched := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id)
	if !matched {
		t.Fatalf("invalid uuidv7: %s", id)
	}
}

func TestUUIDv7MonotonicEnough(t *testing.T) {
	previous := UUIDv7()
	for i := 0; i < 100; i++ {
		next := UUIDv7()
		if strings.Compare(previous, next) >= 0 {
			t.Fatalf("uuid order regressed: %s >= %s", previous, next)
		}
		previous = next
	}
}

// TestCreateTimestampMatchesToISOString locks CreateTimestamp to the JS
// Date.toISOString() shape: UTC, exactly three fractional-second digits, and a
// trailing Z (no nanosecond precision, no trailing-zero trimming).
func TestCreateTimestampMatchesToISOString(t *testing.T) {
	ts := CreateTimestamp()
	matched := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`).MatchString(ts)
	if !matched {
		t.Fatalf("CreateTimestamp() = %q, want ISO8601 with exactly 3 ms digits and Z", ts)
	}
	// Must round-trip through the RFC3339Nano parser used to read timestamps back.
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("CreateTimestamp() = %q is not parseable: %v", ts, err)
	}
}

func TestEncodeSessionCWD(t *testing.T) {
	if got := EncodeSessionCWD("/home/user/projects/pi-go"); got != "--home-user-projects-pi-go--" {
		t.Fatalf("encoded=%q", got)
	}
	if got := EncodeSessionCWD(`C:\work:pi`); got != "--C--work-pi--" {
		t.Fatalf("encoded windows=%q", got)
	}
}

// TestEncodeSessionCWDStripsExactlyOneLeadingSeparator locks EncodeSessionCWD to
// the TS behavior cwd.replace(/^[/\\]/, "") (jsonl-repo.ts:35): exactly ONE
// leading slash OR backslash is removed, and the two separators form a single
// alternation class (so "\/foo" strips only the first "\", leaving "/foo").
func TestEncodeSessionCWDStripsExactlyOneLeadingSeparator(t *testing.T) {
	cases := map[string]string{
		"//foo":           "---foo--",          // second slash survives -> "/foo"
		`\\server\share`:  "---server-share--", // UNC: one backslash survives
		`\/foo`:           "---foo--",          // strip only first "\", "/foo" survives
		`/\foo`:           "---foo--",          // strip only first "/", "\foo" survives
		"/single/leading": "--single-leading--",
	}
	for in, want := range cases {
		if got := EncodeSessionCWD(in); got != want {
			t.Fatalf("EncodeSessionCWD(%q)=%q, want %q", in, got, want)
		}
	}
}
