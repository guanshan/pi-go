package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func UUIDv7() string {
	id, err := uuid.NewV7()
	if err == nil {
		return id.String()
	}
	now := time.Now().UnixNano()
	return fmt.Sprintf("00000000-0000-7000-8000-%012x", uint64(now)&0xffffffffffff)
}

func CreateSessionID() string {
	return UUIDv7()
}

// CreateTimestamp returns the current UTC time in the same fixed-millisecond
// ISO8601 layout as JavaScript's Date.prototype.toISOString() (always exactly
// three fractional digits and a trailing Z), matching session/entry.go's
// iso8601Millis. time.RFC3339Nano trims trailing zeros and can emit up to
// nanosecond precision, which diverges from toISOString, so it must not be used.
func CreateTimestamp() string {
	return time.Now().UTC().Format(iso8601Millis)
}

// iso8601Millis matches JavaScript Date.prototype.toISOString(): UTC with exactly
// three fractional-second digits and a Z suffix.
const iso8601Millis = "2006-01-02T15:04:05.000Z07:00"

func EncodeSessionCWD(cwd string) string {
	clean := strings.TrimPrefix(strings.TrimPrefix(cwd, "/"), `\`)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(clean) + "--"
}
