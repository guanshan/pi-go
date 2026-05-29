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

func CreateTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func EncodeSessionCWD(cwd string) string {
	clean := strings.TrimPrefix(strings.TrimPrefix(cwd, "/"), `\`)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(clean) + "--"
}
