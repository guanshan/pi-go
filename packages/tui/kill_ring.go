package tui

// KillRingPushOptions controls how a new entry merges into the kill ring.
type KillRingPushOptions struct {
	// Prepend selects whether to prepend (backward delete) or append
	// (forward delete) when accumulating into the most recent entry.
	Prepend bool
	// Accumulate merges this push with the most recent entry instead of
	// creating a new ring entry.
	Accumulate bool
}

// KillRing is an Emacs-style kill ring: a stack of killed text entries with
// the most recent at the top. Push, Peek, and Rotate mirror upstream's
// kill-ring.ts.
type KillRing struct {
	ring []string
}

// Push adds text to the ring. Empty text is ignored. When opts.Accumulate is
// true and the ring is non-empty, text is merged into the most recent entry
// (prepended or appended based on opts.Prepend).
func (k *KillRing) Push(text string, opts KillRingPushOptions) {
	if text == "" {
		return
	}
	if opts.Accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		if opts.Prepend {
			k.ring[len(k.ring)-1] = text + last
		} else {
			k.ring[len(k.ring)-1] = last + text
		}
		return
	}
	k.ring = append(k.ring, text)
}

// Peek returns the most recent entry. The boolean is false when the ring is
// empty.
func (k *KillRing) Peek() (string, bool) {
	if len(k.ring) == 0 {
		return "", false
	}
	return k.ring[len(k.ring)-1], true
}

// Rotate moves the most recent entry to the front so the next Peek returns
// the next-older entry. No-op if fewer than 2 entries.
func (k *KillRing) Rotate() {
	if len(k.ring) <= 1 {
		return
	}
	last := k.ring[len(k.ring)-1]
	k.ring = append([]string{last}, k.ring[:len(k.ring)-1]...)
}

// Len returns the number of entries.
func (k *KillRing) Len() int { return len(k.ring) }
