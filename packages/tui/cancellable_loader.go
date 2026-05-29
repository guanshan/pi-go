package tui

// CancellableLoader is a Loader that stops itself on Esc / Ctrl+C and invokes
// the optional OnCancel callback.
type CancellableLoader struct {
	*Loader
	Cancelled bool
	OnCancel  func()
}

// NewCancellableLoader returns a stopped CancellableLoader.
func NewCancellableLoader(label string, indicator LoaderIndicatorOptions, notify func()) *CancellableLoader {
	return &CancellableLoader{Loader: NewLoader(label, indicator, notify)}
}

// HandleInput cancels on Esc or Ctrl+C.
func (c *CancellableLoader) HandleInput(data string) {
	if data == "\x1b" || MatchesKey(data, Ctrl("c")) {
		if !c.Cancelled {
			c.Cancelled = true
			c.Stop()
			if c.OnCancel != nil {
				c.OnCancel()
			}
		}
	}
}
