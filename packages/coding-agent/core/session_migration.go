package core

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/guanshan/pi-go/packages/ai"
)

// migrateSessionEntries upgrades an in-memory session to CurrentSessionVersion,
// mutating header and entries in place. It returns true when anything changed so
// the caller can decide whether to rewrite the file. It mirrors the TypeScript
// migrateToCurrentVersion chain (v1→v2→v3).
func migrateSessionEntries(header *SessionHeader, entries []SessionEntry) bool {
	version := header.Version
	if version <= 0 {
		version = 1
	}
	if version >= CurrentSessionVersion {
		return false
	}
	if version < 2 {
		migrateSessionV1ToV2(entries)
	}
	if version < 3 {
		migrateSessionV2ToV3(entries)
	}
	header.Version = CurrentSessionVersion
	return true
}

// migrateSessionV1ToV2 assigns tree ids/parent links to entries that predate the
// session tree, and converts a legacy compaction firstKeptEntryIndex (an index
// into the on-disk file, where the header occupies index 0) into firstKeptEntryId.
func migrateSessionV1ToV2(entries []SessionEntry) {
	seen := map[string]bool{}
	var previous *string
	for i := range entries {
		entry := &entries[i]
		entry.ID = uniqueSessionEntryID(seen)
		entry.ParentID = previous
		id := entry.ID
		previous = &id
		if entry.Type == "compaction" && entry.FirstKeptEntryIndex != nil {
			// File index 0 is the header; entries here exclude it, so shift by one.
			target := *entry.FirstKeptEntryIndex - 1
			if target >= 0 && target < len(entries) {
				entry.FirstKeptID = entries[target].ID
			}
			entry.FirstKeptEntryIndex = nil
		}
	}
}

// migrateSessionV2ToV3 renames the legacy hookMessage role to custom.
func migrateSessionV2ToV3(entries []SessionEntry) {
	for i := range entries {
		entry := &entries[i]
		if entry.Type != "message" || ai.MessageRole(entry.Message) != "hookMessage" {
			continue
		}
		if custom, ok := ai.AsCustomMessage(entry.Message); ok {
			custom.Role = "custom"
			entry.Message = custom
		}
	}
}

// uniqueSessionEntryID returns a fresh short id not present in seen, matching the
// 4-byte hex ids generated for migrated entries (with a 16-byte fallback).
func uniqueSessionEntryID(seen map[string]bool) string {
	for i := 0; i < 101; i++ {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			continue
		}
		id := hex.EncodeToString(b[:4])
		if i == 100 {
			id = hex.EncodeToString(b[:])
		}
		if !seen[id] {
			seen[id] = true
			return id
		}
	}
	return "session-entry"
}
