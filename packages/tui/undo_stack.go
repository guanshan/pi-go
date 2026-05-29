package tui

// UndoStack is a generic stack used by editor components for undo/redo. The
// caller is responsible for snapshotting state before mutations, and for
// deep-copying any reference-typed fields inside T.
type UndoStack[T any] struct {
	stack []T
}

// Push appends snapshot to the stack.
func (u *UndoStack[T]) Push(snapshot T) {
	u.stack = append(u.stack, snapshot)
}

// Pop returns the most recent snapshot and removes it from the stack. The
// boolean is false when the stack is empty.
func (u *UndoStack[T]) Pop() (T, bool) {
	var zero T
	if len(u.stack) == 0 {
		return zero, false
	}
	last := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return last, true
}

// Peek returns the most recent snapshot without removing it.
func (u *UndoStack[T]) Peek() (T, bool) {
	var zero T
	if len(u.stack) == 0 {
		return zero, false
	}
	return u.stack[len(u.stack)-1], true
}

// Clear empties the stack.
func (u *UndoStack[T]) Clear() { u.stack = u.stack[:0] }

// Len returns the number of snapshots on the stack.
func (u *UndoStack[T]) Len() int { return len(u.stack) }
