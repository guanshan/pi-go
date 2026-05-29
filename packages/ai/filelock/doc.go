// Package filelock provides a small cross-process advisory lock used to
// serialize writes to shared on-disk state (notably auth.json). It uses atomic
// lock-directory creation with a stale-steal timeout and a background heartbeat
// so a slow operation under the lock (e.g. a network OAuth refresh) is not
// mistaken for an abandoned lock. It is a leaf package with no pi dependencies.
package filelock
