package messagequeue

import (
	"testing"

	"github.com/guanshan/pi-go/packages/ai"
)

func TestQueueDrainModes(t *testing.T) {
	one := New("")
	one.Enqueue(ai.NewUserMessage("one", nil))
	one.Enqueue(ai.NewUserMessage("two", nil))
	if got := one.Drain(); len(got) != 1 || ai.MessageText(got[0]) != "one" {
		t.Fatalf("one-at-a-time first=%#v", got)
	}
	if got := one.Drain(); len(got) != 1 || ai.MessageText(got[0]) != "two" {
		t.Fatalf("one-at-a-time second=%#v", got)
	}

	all := New(ModeAll)
	all.Enqueue(ai.NewUserMessage("one", nil))
	all.Enqueue(ai.NewUserMessage("two", nil))
	if got := all.Drain(); len(got) != 2 || ai.MessageText(got[0]) != "one" || ai.MessageText(got[1]) != "two" {
		t.Fatalf("all=%#v", got)
	}
}

func TestQueuePrependAndClear(t *testing.T) {
	queue := New(ModeAll)
	queue.Enqueue(ai.NewUserMessage("tail", nil))
	queue.Prepend([]ai.Message{ai.NewUserMessage("head", nil)})
	if got := queue.Snapshot(); len(got) != 2 || ai.MessageText(got[0]) != "head" {
		t.Fatalf("snapshot=%#v", got)
	}
	if cleared := queue.Clear(); len(cleared) != 2 || queue.HasItems() {
		t.Fatalf("cleared=%#v len=%d", cleared, queue.Len())
	}
}
