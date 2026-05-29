package tui

import "testing"

// staticComponent only implements Render — used to verify Container handles
// children that don't expose HandleInput / Invalidate.
type staticComponent struct {
	rendered string
	calls    *int
}

func (s *staticComponent) Render(width int) []string {
	if s.calls != nil {
		*s.calls++
	}
	return []string{s.rendered}
}

// dynamicComponent implements every optional capability and records calls.
type dynamicComponent struct {
	rendered     string
	inputs       []string
	invalidated  int
	focused      bool
	wantsRelease bool
}

func (d *dynamicComponent) Render(width int) []string { return []string{d.rendered} }
func (d *dynamicComponent) HandleInput(data string)   { d.inputs = append(d.inputs, data) }
func (d *dynamicComponent) Invalidate()               { d.invalidated++ }
func (d *dynamicComponent) SetFocused(b bool)         { d.focused = b }
func (d *dynamicComponent) Focused() bool             { return d.focused }
func (d *dynamicComponent) WantsKeyRelease() bool     { return d.wantsRelease }

func TestComponentInterfaceDispatch(t *testing.T) {
	stat := &staticComponent{rendered: "static"}
	dyn := &dynamicComponent{rendered: "dyn"}

	// HandleComponentInput should return false on a Render-only component
	// (no panic, no error).
	if HandleComponentInput(stat, "x") {
		t.Error("static should not accept input")
	}
	if !HandleComponentInput(dyn, "y") {
		t.Error("dynamic should accept input")
	}
	if len(dyn.inputs) != 1 || dyn.inputs[0] != "y" {
		t.Errorf("inputs: %#v", dyn.inputs)
	}

	// InvalidateComponent must not panic on Render-only.
	InvalidateComponent(stat)
	InvalidateComponent(dyn)
	if dyn.invalidated != 1 {
		t.Errorf("invalidated: %d", dyn.invalidated)
	}

	// IsFocusable should distinguish.
	if _, ok := IsFocusable(stat); ok {
		t.Error("static should not be focusable")
	}
	if f, ok := IsFocusable(dyn); !ok {
		t.Error("dynamic should be focusable")
	} else {
		f.SetFocused(true)
		if !dyn.focused {
			t.Error("focus flag not propagated")
		}
	}
}

func TestContainerInvalidateDispatchesOptional(t *testing.T) {
	stat := &staticComponent{rendered: "x"}
	dyn := &dynamicComponent{rendered: "y"}
	c := &Container{Children: []Component{stat, dyn}}
	c.Invalidate()
	if dyn.invalidated != 1 {
		t.Errorf("dyn invalidated: %d", dyn.invalidated)
	}
}

func TestContainerRenderRespectsRenderOnlyChildren(t *testing.T) {
	calls := 0
	stat := &staticComponent{rendered: "hello", calls: &calls}
	c := &Container{Children: []Component{stat}}
	out := c.Render(20)
	if len(out) != 1 || out[0] != "hello" {
		t.Errorf("render: %#v", out)
	}
	if calls != 1 {
		t.Errorf("Render calls: %d", calls)
	}
}

func TestIsFocusableNil(t *testing.T) {
	if _, ok := IsFocusable(nil); ok {
		t.Error("nil should not be focusable")
	}
}
