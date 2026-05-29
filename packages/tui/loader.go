package tui

import (
	"fmt"
	"sync"
	"time"
)

// LoaderIndicatorOptions controls the loader's animation frames.
type LoaderIndicatorOptions struct {
	// Frames is the animation loop. nil → DefaultLoaderFrames.
	Frames []string
	// Interval between frames. <= 0 → 80ms.
	Interval time.Duration
}

// DefaultLoaderFrames returns a copy of the default Braille spinner used
// when LoaderIndicatorOptions.Frames is nil. It returns a fresh slice on each
// call so callers can safely mutate the result.
func DefaultLoaderFrames() []string {
	out := make([]string, len(defaultLoaderFrames))
	copy(out, defaultLoaderFrames)
	return out
}

var defaultLoaderFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultLoaderInterval = 80 * time.Millisecond

// Loader is an animated indicator with a label. The animation is driven by
// an internal goroutine; call Start to begin and Stop to halt.
type Loader struct {
	mu       sync.Mutex
	label    string
	frames   []string
	interval time.Duration
	frame    int
	stop     chan struct{}
	done     chan struct{}
	notify   func() // optional re-render callback (e.g. tea.Cmd / RequestRender)
}

// NewLoader returns a stopped Loader. Call Start to begin animation; pass a
// notify callback (e.g. invokable from a Bubble Tea Cmd) so the host can
// trigger re-renders on each tick. notify may be nil.
func NewLoader(label string, indicator LoaderIndicatorOptions, notify func()) *Loader {
	frames := indicator.Frames
	if frames == nil {
		frames = DefaultLoaderFrames()
	}
	interval := indicator.Interval
	if interval <= 0 {
		interval = defaultLoaderInterval
	}
	return &Loader{
		label:    label,
		frames:   frames,
		interval: interval,
		notify:   notify,
	}
}

// Render produces a single line: "<frame> <label>".
func (l *Loader) Render(width int) []string {
	l.mu.Lock()
	frame := ""
	if len(l.frames) > 0 {
		frame = l.frames[l.frame%len(l.frames)]
	}
	label := l.label
	if label == "" {
		label = "Loading"
	}
	l.mu.Unlock()
	if frame == "" {
		return []string{TruncateToWidth(label, width, "...")}
	}
	return []string{TruncateToWidth(fmt.Sprintf("%s %s", frame, label), width, "...")}
}

// SetLabel updates the displayed label and triggers notify if set.
func (l *Loader) SetLabel(label string) {
	l.mu.Lock()
	l.label = label
	notify := l.notify
	l.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// SetIndicator replaces the animation frames / interval.
func (l *Loader) SetIndicator(opts LoaderIndicatorOptions) {
	l.mu.Lock()
	l.frame = 0
	l.frames = opts.Frames
	if l.frames == nil {
		l.frames = DefaultLoaderFrames()
	}
	l.interval = opts.Interval
	if l.interval <= 0 {
		l.interval = defaultLoaderInterval
	}
	l.mu.Unlock()
}

// Start begins ticking. No-op if already started.
func (l *Loader) Start() {
	l.mu.Lock()
	if l.stop != nil || len(l.frames) <= 1 {
		l.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := l.interval
	notify := l.notify
	l.stop = stop
	l.done = done
	l.mu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				l.mu.Lock()
				l.frame = (l.frame + 1) % maxInt(1, len(l.frames))
				l.mu.Unlock()
				if notify != nil {
					notify()
				}
			case <-stop:
				return
			}
		}
	}()
}

// Stop halts the ticker. No-op if not started.
func (l *Loader) Stop() {
	l.mu.Lock()
	stop := l.stop
	done := l.done
	l.stop = nil
	l.done = nil
	l.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// Label returns the current label (legacy field accessor).
func (l *Loader) Label() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.label
}
