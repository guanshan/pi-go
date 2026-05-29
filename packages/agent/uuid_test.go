package agent

import (
	"regexp"
	"strings"
	"testing"
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

func TestEncodeSessionCWD(t *testing.T) {
	if got := EncodeSessionCWD("/home/user/projects/pi-go"); got != "--home-user-projects-pi-go--" {
		t.Fatalf("encoded=%q", got)
	}
	if got := EncodeSessionCWD(`C:\work:pi`); got != "--C--work-pi--" {
		t.Fatalf("encoded windows=%q", got)
	}
}
