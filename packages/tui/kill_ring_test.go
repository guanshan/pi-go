package tui

import "testing"

func peekVal(t *testing.T, k *KillRing) string {
	t.Helper()
	v, _ := k.Peek()
	return v
}

func TestKillRing(t *testing.T) {
	var k KillRing
	k.Push("hello", KillRingPushOptions{})
	k.Push(" world", KillRingPushOptions{})
	if peekVal(t, &k) != " world" {
		t.Errorf("peek = %q", peekVal(t, &k))
	}
	k.Rotate()
	if peekVal(t, &k) != "hello" {
		t.Errorf("after rotate: %q", peekVal(t, &k))
	}
	if k.Len() != 2 {
		t.Errorf("len: %d", k.Len())
	}
}

func TestKillRingAccumulate(t *testing.T) {
	var k KillRing
	k.Push("foo", KillRingPushOptions{})
	k.Push("bar", KillRingPushOptions{Accumulate: true})
	if peekVal(t, &k) != "foobar" {
		t.Errorf("append accumulate: %q", peekVal(t, &k))
	}
	if k.Len() != 1 {
		t.Errorf("len: %d", k.Len())
	}
	k.Push("baz", KillRingPushOptions{Accumulate: true, Prepend: true})
	if peekVal(t, &k) != "bazfoobar" {
		t.Errorf("prepend accumulate: %q", peekVal(t, &k))
	}
}

func TestKillRingEmpty(t *testing.T) {
	var k KillRing
	if v, ok := k.Peek(); ok || v != "" {
		t.Errorf("empty peek: %q %v", v, ok)
	}
	k.Rotate() // no-op
	k.Push("", KillRingPushOptions{})
	if k.Len() != 0 {
		t.Error("empty push should be no-op")
	}
}

func TestUndoStack(t *testing.T) {
	var s UndoStack[int]
	if _, ok := s.Pop(); ok {
		t.Error("pop empty")
	}
	s.Push(1)
	s.Push(2)
	s.Push(3)
	if s.Len() != 3 {
		t.Errorf("len: %d", s.Len())
	}
	if v, _ := s.Pop(); v != 3 {
		t.Errorf("pop 3: %d", v)
	}
	if v, _ := s.Peek(); v != 2 {
		t.Errorf("peek 2: %d", v)
	}
	s.Clear()
	if s.Len() != 0 {
		t.Error("clear")
	}
}
