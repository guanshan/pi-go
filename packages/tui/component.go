package tui

// CursorMarker is the zero-width APC sequence components emit at the cursor
// position when focused. Embedders look for it to position the hardware
// cursor (used for IME candidate window placement). Mirrors upstream's
// CURSOR_MARKER constant.
const CursorMarker = "\x1b_pi:c\a"

// Component is the minimal contract every visual unit implements: render to
// a list of lines for the given visible width.
//
// Optional capabilities (input handling, invalidation, focus, key-release
// opt-in) are advertised via separate interfaces and discovered with type
// assertions — see InputHandler, Invalidator, Focusable, WantsKeyRelease.
//
// This mirrors how upstream's TypeScript Component declares optional
// `handleInput?` / `invalidate?` properties.
type Component interface {
	Render(width int) []string
}

// InputHandler is the optional interface a Component implements when it
// wants raw input events.
type InputHandler interface {
	HandleInput(data string)
}

// Invalidator is the optional interface a Component implements when it has
// cached render state that should be dropped on theme/data changes.
type Invalidator interface {
	Invalidate()
}

// Focusable is implemented by components that can receive focus and want to
// be told when their focus state changes.
type Focusable interface {
	SetFocused(bool)
	Focused() bool
}

// WantsKeyRelease is implemented by components that want to receive Kitty
// key-release events. The default behaviour is to drop them. Components opt
// in by returning true.
type WantsKeyRelease interface {
	WantsKeyRelease() bool
}

// HandleComponentInput dispatches data to c when it implements InputHandler.
// It is a small helper so callers don't have to repeat the type assertion.
func HandleComponentInput(c Component, data string) bool {
	h, ok := c.(InputHandler)
	if !ok {
		return false
	}
	h.HandleInput(data)
	return true
}

// InvalidateComponent calls c.Invalidate when implemented.
func InvalidateComponent(c Component) {
	if v, ok := c.(Invalidator); ok {
		v.Invalidate()
	}
}

// IsFocusable returns the Focusable view of c if it implements the interface.
func IsFocusable(c Component) (Focusable, bool) {
	if c == nil {
		return nil, false
	}
	f, ok := c.(Focusable)
	return f, ok
}

// Container is a pure compositional primitive: render its children top-to-
// bottom for the given width. It is NOT a renderer or event loop —
// embedders (e.g. Bubble Tea programs) drive rendering and input themselves.
type Container struct {
	Children []Component
}

// AddChild appends a child.
func (c *Container) AddChild(child Component) {
	c.Children = append(c.Children, child)
}

// RemoveChild removes the first occurrence of child (by interface identity).
func (c *Container) RemoveChild(child Component) {
	for i, existing := range c.Children {
		if existing == child {
			c.Children = append(c.Children[:i], c.Children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() { c.Children = nil }

// Invalidate forwards Invalidate to every child that implements
// Invalidator.
func (c *Container) Invalidate() {
	for _, child := range c.Children {
		InvalidateComponent(child)
	}
}

// Render concatenates the rendered lines of every child.
func (c *Container) Render(width int) []string {
	var lines []string
	for _, child := range c.Children {
		lines = append(lines, child.Render(width)...)
	}
	if lines == nil {
		return []string{}
	}
	return lines
}
