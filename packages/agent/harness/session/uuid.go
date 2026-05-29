package session

import (
	"fmt"
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
