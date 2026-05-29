package utils

import (
	"errors"
	"sync"
)

type SessionResourceCleanup func(sessionID string) error

var cleanupRegistry = struct {
	sync.Mutex
	next  int
	items map[int]SessionResourceCleanup
}{items: map[int]SessionResourceCleanup{}}

func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	cleanupRegistry.Lock()
	cleanupRegistry.next++
	id := cleanupRegistry.next
	cleanupRegistry.items[id] = cleanup
	cleanupRegistry.Unlock()
	return func() {
		cleanupRegistry.Lock()
		delete(cleanupRegistry.items, id)
		cleanupRegistry.Unlock()
	}
}

func CleanupSessionResources(sessionID string) error {
	cleanupRegistry.Lock()
	items := make([]SessionResourceCleanup, 0, len(cleanupRegistry.items))
	for _, cleanup := range cleanupRegistry.items {
		items = append(items, cleanup)
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
