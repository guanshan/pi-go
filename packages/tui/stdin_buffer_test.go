package tui

import (
	"sync"
	"testing"
	"time"
)

func collect(t *testing.T) (*StdinBuffer, *[]string, *[]string) {
	t.Helper()
	var data []string
	var paste []string
	var mu sync.Mutex
	b := &StdinBuffer{
		OnData: func(s string) {
			mu.Lock()
			defer mu.Unlock()
			data = append(data, s)
		},
		OnPaste: func(s string) {
			mu.Lock()
			defer mu.Unlock()
			paste = append(paste, s)
		},
	}
	return b, &data, &paste
}

func TestStdinBufferSimpleSequences(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b[A")
	if len(*data) != 1 || (*data)[0] != "\x1b[A" {
		t.Errorf("got %#v", *data)
	}
}

func TestStdinBufferSplitArrival(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b")
	if len(*data) != 0 {
		t.Errorf("after partial: %#v", *data)
	}
	b.Process("[")
	if len(*data) != 0 {
		t.Errorf("after [: %#v", *data)
	}
	b.Process("A")
	if len(*data) != 1 || (*data)[0] != "\x1b[A" {
		t.Errorf("after A: %#v", *data)
	}
}

func TestStdinBufferSplitWithMixedContent(t *testing.T) {
	b, data, _ := collect(t)
	// Bytes "ab\x1b[" arrive, then "C" finishes the right-arrow.
	b.Process("ab\x1b[")
	b.Process("C")
	want := []string{"a", "b", "\x1b[C"}
	if len(*data) != len(want) {
		t.Fatalf("got %#v", *data)
	}
	for i, w := range want {
		if (*data)[i] != w {
			t.Errorf("[%d] got %q want %q", i, (*data)[i], w)
		}
	}
}

func TestStdinBufferBracketedPaste(t *testing.T) {
	b, data, paste := collect(t)
	b.Process("hi\x1b[200~hello world\x1b[201~bye")
	if len(*paste) != 1 || (*paste)[0] != "hello world" {
		t.Errorf("paste = %#v", *paste)
	}
	if len(*data) == 0 {
		t.Errorf("expected pre/post paste data")
	}
}

func TestStdinBufferBracketedPasteSplit(t *testing.T) {
	b, _, paste := collect(t)
	b.Process("\x1b[200~part1 ")
	if len(*paste) != 0 {
		t.Errorf("partial paste should not emit: %#v", *paste)
	}
	b.Process("part2\x1b[201~")
	if len(*paste) != 1 || (*paste)[0] != "part1 part2" {
		t.Errorf("paste: %#v", *paste)
	}
}

func TestStdinBufferBracketedPasteWithEscapeInside(t *testing.T) {
	b, _, paste := collect(t)
	// Paste content may legitimately contain ESC characters — they should
	// be passed through verbatim until the closing marker.
	b.Process("\x1b[200~hello \x1b[31mred\x1b[0m world\x1b[201~")
	if len(*paste) != 1 || (*paste)[0] != "hello \x1b[31mred\x1b[0m world" {
		t.Errorf("paste w/ ANSI: %#v", *paste)
	}
}

func TestStdinBufferSGRMouse(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b[<35;20;5M")
	if len(*data) != 1 || (*data)[0] != "\x1b[<35;20;5M" {
		t.Errorf("SGR mouse: %#v", *data)
	}
}

func TestStdinBufferSGRMouseRelease(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b[<0;20;5m")
	if len(*data) != 1 || (*data)[0] != "\x1b[<0;20;5m" {
		t.Errorf("SGR mouse release: %#v", *data)
	}
}

func TestStdinBufferSGRMouseIncomplete(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b[<35;20")
	if len(*data) != 0 {
		t.Errorf("incomplete should buffer: %#v", *data)
	}
	b.Process(";5M")
	if len(*data) != 1 || (*data)[0] != "\x1b[<35;20;5M" {
		t.Errorf("after complete: %#v", *data)
	}
}

func TestStdinBufferOldStyleMouse(t *testing.T) {
	b, data, _ := collect(t)
	// ESC [ M + 3 raw bytes
	b.Process("\x1b[M\x20\x21\x22")
	if len(*data) != 1 || (*data)[0] != "\x1b[M\x20\x21\x22" {
		t.Errorf("old-style mouse: %#v", *data)
	}
}

func TestStdinBufferAPCKitty(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b_Gi=1\x1b\\")
	if len(*data) != 1 || (*data)[0] != "\x1b_Gi=1\x1b\\" {
		t.Errorf("APC: %#v", *data)
	}
}

func TestStdinBufferAPCSplit(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b_Gi=42,")
	b.Process("a=T")
	b.Process("\x1b\\")
	if len(*data) != 1 || (*data)[0] != "\x1b_Gi=42,a=T\x1b\\" {
		t.Errorf("APC split: %#v", *data)
	}
}

func TestStdinBufferOSC(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b]0;title\x07")
	if len(*data) != 1 || (*data)[0] != "\x1b]0;title\x07" {
		t.Errorf("OSC BEL terminator: %#v", *data)
	}
}

func TestStdinBufferOSCESCSlash(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1b]52;c;SGVsbG8=\x1b\\")
	if len(*data) != 1 {
		t.Errorf("OSC ST terminator: %#v", *data)
	}
}

func TestStdinBufferDCS(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1bP>|\x1b\\")
	if len(*data) != 1 || (*data)[0] != "\x1bP>|\x1b\\" {
		t.Errorf("DCS: %#v", *data)
	}
}

func TestStdinBufferSS3(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("\x1bOP")
	if len(*data) != 1 || (*data)[0] != "\x1bOP" {
		t.Errorf("SS3 F1: %#v", *data)
	}
}

func TestStdinBufferEscapeEscapeFollowedByCSI(t *testing.T) {
	b, data, _ := collect(t)
	// \x1b\x1b[27;...u — should produce ESC then a CSI-u sequence.
	b.Process("\x1b\x1b[27;5;65~")
	if len(*data) != 2 {
		t.Errorf("want 2 sequences, got %#v", *data)
	} else if (*data)[0] != "\x1b" || (*data)[1] != "\x1b[27;5;65~" {
		t.Errorf("got %#v", *data)
	}
}

func TestStdinBufferEscapeEscapeMetaKey(t *testing.T) {
	b, data, _ := collect(t)
	// Plain alt+escape: ESC ESC followed by a non-special char should be one
	// 2-byte meta sequence.
	b.Process("\x1b\x1ba")
	if len(*data) != 2 {
		t.Errorf("got %#v", *data)
	}
	// First should be alt+escape, second 'a'.
	if (*data)[0] != "\x1b\x1b" || (*data)[1] != "a" {
		t.Errorf("got %#v", *data)
	}
}

func TestStdinBufferKittyPrintableDedup(t *testing.T) {
	b, data, _ := collect(t)
	// Send the CSI-u form and then the plain rune; the second should be
	// suppressed because its codepoint matches the pending one.
	b.Process("\x1b[97u")
	b.Process("a")
	if len(*data) != 1 || (*data)[0] != "\x1b[97u" {
		t.Errorf("dedup: %#v", *data)
	}
	// A non-matching rune passes through.
	b.Process("b")
	if len(*data) != 2 || (*data)[1] != "b" {
		t.Errorf("after non-match: %#v", *data)
	}
}

func TestStdinBufferPlainText(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("hello")
	if len(*data) != 5 {
		t.Errorf("plain text: %#v", *data)
	}
}

func TestStdinBufferUnicode(t *testing.T) {
	b, data, _ := collect(t)
	b.Process("你好")
	if len(*data) != 2 {
		t.Errorf("UTF-8 split into runes: %#v", *data)
	}
	if (*data)[0] != "你" || (*data)[1] != "好" {
		t.Errorf("got %#v", *data)
	}
}

func TestStdinBufferTimeoutFlush(t *testing.T) {
	var data []string
	var mu sync.Mutex
	b := &StdinBuffer{
		Timeout: 5 * time.Millisecond,
		OnData: func(s string) {
			mu.Lock()
			defer mu.Unlock()
			data = append(data, s)
		},
	}
	b.Process("\x1b[")
	mu.Lock()
	if len(data) != 0 {
		mu.Unlock()
		t.Fatalf("buffered: %#v", data)
	}
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(data) != 1 || data[0] != "\x1b[" {
		t.Errorf("flush: %#v", data)
	}
}

func TestStdinBufferClearFlush(t *testing.T) {
	b := &StdinBuffer{}
	b.Process("\x1b[")
	b.Clear()
	b.Process("[")
	// after Clear the buffer should be empty, so [ is just a literal "["
	// (no escape prefix).
	// We don't have OnData here; just ensure Clear/Destroy don't panic.
	b.Destroy()
}

func TestStdinBufferConcurrentSafe(t *testing.T) {
	b := &StdinBuffer{OnData: func(string) {}}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Process("\x1b[A")
			}
		}()
	}
	wg.Wait()
}
