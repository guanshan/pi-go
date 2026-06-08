package utils

import (
	"errors"
	"sync"
)

type SessionResourceCleanup func(sessionID string) error

// cleanupEntry pairs a registered cleanup with a monotonic id so it can be
// removed on unregister while preserving registration (insertion) order, which
// mirrors the TS Set<SessionResourceCleanup> iteration semantics.
type cleanupEntry struct {
	id      int
	cleanup SessionResourceCleanup
}

var cleanupRegistry = struct {
	sync.Mutex
	next  int
	items []cleanupEntry
}{}

func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	cleanupRegistry.Lock()
	cleanupRegistry.next++
	id := cleanupRegistry.next
	cleanupRegistry.items = append(cleanupRegistry.items, cleanupEntry{id: id, cleanup: cleanup})
	cleanupRegistry.Unlock()
	return func() {
		cleanupRegistry.Lock()
		for i, entry := range cleanupRegistry.items {
			if entry.id == id {
				cleanupRegistry.items = append(cleanupRegistry.items[:i], cleanupRegistry.items[i+1:]...)
				break
			}
		}
		cleanupRegistry.Unlock()
	}
}

func CleanupSessionResources(sessionID string) error {
	cleanupRegistry.Lock()
	items := make([]SessionResourceCleanup, 0, len(cleanupRegistry.items))
	for _, entry := range cleanupRegistry.items {
		items = append(items, entry.cleanup)
	}
	cleanupRegistry.Unlock()
	var errs []error
	for _, cleanup := range items {
		if err := cleanup(sessionID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
