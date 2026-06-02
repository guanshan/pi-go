package tui

import (
	"strings"
	"testing"
)

func TestDetectCapabilitiesFromEnv(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		images     ImageProtocol
		trueColor  bool
		hyperlinks bool
	}{
		{name: "unknown", env: map[string]string{}, images: ImageProtocolNone, trueColor: false, hyperlinks: false},
		{name: "truecolor hint", env: map[string]string{"COLORTERM": "truecolor"}, images: ImageProtocolNone, trueColor: true, hyperlinks: false},
		{name: "tmux blocks images and hyperlinks by default", env: map[string]string{"TMUX": "/tmp/tmux", "TERM_PROGRAM": "ghostty"}, images: ImageProtocolNone, trueColor: false, hyperlinks: false},
		{name: "tmux trusts explicit truecolor", env: map[string]string{"TERM": "tmux-256color", "COLORTERM": "24bit"}, images: ImageProtocolNone, trueColor: true, hyperlinks: false},
		{name: "kitty", env: map[string]string{"KITTY_WINDOW_ID": "1"}, images: ImageProtocolKitty, trueColor: true, hyperlinks: true},
		{name: "ghostty", env: map[string]string{"TERM_PROGRAM": "ghostty"}, images: ImageProtocolKitty, trueColor: true, hyperlinks: true},
		{name: "wezterm", env: map[string]string{"WEZTERM_PANE": "1"}, images: ImageProtocolKitty, trueColor: true, hyperlinks: true},
		{name: "iterm2", env: map[string]string{"TERM_PROGRAM": "iterm.app"}, images: ImageProtocolITerm2, trueColor: true, hyperlinks: true},
		{name: "windows terminal", env: map[string]string{"WT_SESSION": "abc"}, images: ImageProtocolNone, trueColor: true, hyperlinks: true},
		{name: "vscode", env: map[string]string{"TERM_PROGRAM": "vscode"}, images: ImageProtocolNone, trueColor: true, hyperlinks: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := DetectCapabilitiesFromEnv(tt.env)
			if caps.Images != tt.images || caps.TrueColor != tt.trueColor || caps.Hyperlinks != tt.hyperlinks {
				t.Fatalf("caps=%#v", caps)
			}
			if (caps.Images == ImageProtocolKitty) != caps.Kitty {
				t.Fatalf("kitty compatibility flag mismatch: %#v", caps)
			}
			if (caps.Images == ImageProtocolITerm2) != caps.ITerm2 {
				t.Fatalf("iterm2 compatibility flag mismatch: %#v", caps)
			}
		})
	}
}

func TestDetectCapabilitiesFromEnvTmuxHyperlinkProbe(t *testing.T) {
	caps := DetectCapabilitiesFromEnvWithTmuxProbe(map[string]string{"TMUX": "/tmp/tmux", "COLORTERM": "truecolor"}, func() bool {
		return true
	})
	if caps.Images != ImageProtocolNone || !caps.TrueColor || !caps.Hyperlinks {
		t.Fatalf("tmux forwarded caps=%#v", caps)
	}
	caps = DetectCapabilitiesFromEnvWithTmuxProbe(map[string]string{"TERM": "screen-256color", "COLORTERM": "truecolor"}, func() bool {
		t.Fatal("screen should not call tmux probe")
		return true
	})
	if caps.Images != ImageProtocolNone || !caps.TrueColor || caps.Hyperlinks {
		t.Fatalf("screen caps=%#v", caps)
	}

	// tmux that does not forward OSC 8 keeps hyperlinks off.
	caps = DetectCapabilitiesFromEnvWithTmuxProbe(map[string]string{"TERM": "tmux-256color"}, func() bool {
		return false
	})
	if caps.Images != ImageProtocolNone || caps.TrueColor || caps.Hyperlinks {
		t.Fatalf("tmux non-forwarding caps=%#v", caps)
	}

	// A nil probe must not panic and defaults hyperlinks off.
	caps = DetectCapabilitiesFromEnvWithTmuxProbe(map[string]string{"TMUX": "/tmp/tmux"}, nil)
	if caps.Hyperlinks {
		t.Fatalf("nil probe should leave hyperlinks off: %#v", caps)
	}
}

func TestTerminalImageProtocolsAndHyperlinks(t *testing.T) {
	defer ResetCapabilitiesCache()
	SetCellDimensions(CellDimensions{Width: 10, Height: 10})
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	noMove := false
	seq := EncodeKitty([]byte("data"), ImageRenderOptions{ID: 42, Width: 2, Height: 3, MoveCursor: &noMove})
	if !strings.HasPrefix(seq, "\x1b_Ga=T,f=100,q=2,C=1,c=2,r=3,i=42;") {
		t.Fatalf("kitty seq=%q", seq)
	}
	if DeleteKittyImage(42) != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
		t.Fatal("delete kitty image sequence mismatch")
	}
	if DeleteAllKittyImages() != "\x1b_Ga=d,d=A,q=2\x1b\\" {
		t.Fatal("delete all kitty images sequence mismatch")
	}
	if !strings.HasPrefix(RenderImage([]byte("data"), ImageRenderOptions{ID: 7}), "\x1b_G") {
		t.Fatal("render image did not use kitty capabilities")
	}

	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true})
	if !strings.HasPrefix(RenderImage([]byte("data"), ImageRenderOptions{}), "\x1b]1337;File=") {
		t.Fatal("render image did not use iTerm2 capabilities")
	}
	if got := Hyperlink("click me", "https://example.com"); got != "\x1b]8;;https://example.com\x1b\\click me\x1b]8;;\x1b\\" {
		t.Fatalf("hyperlink=%q", got)
	}
}

func TestEncodeKittyChunksLargePayloads(t *testing.T) {
	data := []byte(strings.Repeat("x", 4096))
	seq := EncodeKitty(data, ImageRenderOptions{ID: 99})
	if !strings.Contains(seq, ",m=1;") {
		t.Fatalf("first chunk missing m=1: %q", seq[:min(len(seq), 120)])
	}
	if !strings.Contains(seq, "\x1b_Gm=0;") {
		t.Fatalf("last chunk missing m=0: %q", seq[len(seq)-min(len(seq), 120):])
	}
	if strings.Count(seq, "\x1b_G") < 2 {
		t.Fatalf("expected multiple kitty chunks: %q", seq)
	}
}

func TestIsImageLineAndCellSize(t *testing.T) {
	if !IsImageLine("prefix \x1b]1337;File=inline=1:data\x07") {
		t.Fatal("iTerm2 image line not detected")
	}
	if !IsImageLine("prefix \x1b_Ga=T,f=100;AAAA\x1b\\") {
		t.Fatal("kitty image line not detected")
	}
	if IsImageLine("/tmp/File_1337_backup.png") {
		t.Fatal("plain path falsely detected as image")
	}

	SetCellDimensions(CellDimensions{Width: 10, Height: 10})
	size := CalculateImageCellSize(ImageDimensions{Width: 10, Height: 100}, 10, 5)
	if size.Columns != 1 || size.Rows != 5 {
		t.Fatalf("cell size=%#v", size)
	}
}
