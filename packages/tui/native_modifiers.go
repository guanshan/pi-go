package tui

import (
	"sync"
)

// ModifierKey identifies a modifier key recognised by native helpers.
type ModifierKey string

const (
	ModifierShift   ModifierKey = "shift"
	ModifierCommand ModifierKey = "command"
	ModifierControl ModifierKey = "control"
	ModifierOption  ModifierKey = "option"
)

// NativeModifiersHelper reports whether a modifier key is currently pressed
// using a platform-native API (e.g. CGEventSource on macOS).
type NativeModifiersHelper interface {
	IsModifierPressed(ModifierKey) bool
}

var (
	nativeModifiersMu     sync.RWMutex
	nativeModifiersHelper NativeModifiersHelper
)

// SetNativeModifiersHelper installs (or clears) the process-wide modifiers
// helper. Pass nil to remove it. Safe for concurrent use.
func SetNativeModifiersHelper(helper NativeModifiersHelper) {
	nativeModifiersMu.Lock()
	nativeModifiersHelper = helper
	nativeModifiersMu.Unlock()
}

// IsNativeModifierPressed delegates to the installed helper. Returns false
// when no helper is installed, on non-darwin platforms with no helper, or
// when the modifier name is unknown.
func IsNativeModifierPressed(key ModifierKey) bool {
	switch key {
	case ModifierShift, ModifierCommand, ModifierControl, ModifierOption:
		// known
	default:
		return false
	}
	nativeModifiersMu.RLock()
	helper := nativeModifiersHelper
	nativeModifiersMu.RUnlock()
	// On non-darwin platforms helper is nil unless one was explicitly injected
	// (test setup); production builds expect darwin.
	if helper == nil {
		return false
	}
	return helper.IsModifierPressed(key)
}
