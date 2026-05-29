// Package tui provides the Go port of @earendil-works/pi-tui — a small set of
// terminal primitives, input parsers, and leaf components that pi-go shares
// with its TypeScript upstream.
//
// # Position in the architecture
//
// pi-go's interactive coding-agent UI is driven by charm.land/bubbletea/v2.
// This package therefore does NOT provide a primary event loop, raw-mode
// stdin reader, differential renderer, or overlay manager. The legacy
// "TUI" type and its overlay machinery were removed; embedders that need
// a renderer should use Bubble Tea (or any other framework) and call into
// the helpers exported here.
//
// What this package does provide:
//
//   - Width-aware string utilities: VisibleWidth, TruncateToWidth,
//     WrapTextWithANSI, SliceByColumn, SliceWithWidth, ApplyBackgroundToLine,
//     ExtractAnsiCode, NormalizeTerminalOutput.
//   - Grapheme-cluster awareness via github.com/rivo/uniseg, so wide CJK
//     characters and emoji ZWJ sequences count and slice correctly.
//   - Word navigation: FindWordBackward, FindWordForward.
//   - Keyboard input parsing: KeyID / Key constants, MatchesKey, ParseKey,
//     IsKeyRelease, IsKeyRepeat, DecodeKittyPrintable, DecodePrintableKey
//     (covers legacy CSI sequences, Kitty CSI-u with disambiguation +
//     event types + alternate keys, and xterm modifyOtherKeys).
//   - Keybinding registry: KeybindingsManager, TUIKeybindings, conflict
//     detection.
//   - StdinBuffer: a ported version of @earendil-works/pi-tui's
//     stdin-buffer.ts that splits raw stdin into complete escape sequences
//     and bracketed-paste payloads (CSI / OSC / DCS / APC / SS3 / SGR
//     mouse + bracketed paste + Kitty pending dedup).
//   - Terminal-capability detection (Kitty / iTerm2 / Ghostty / WezTerm /
//     Apple Terminal / Hyper / VS Code / Alacritty / JetBrains JediTerm /
//     tmux / screen) and image-protocol encoding (Kitty graphics, iTerm2
//     inline images, OSC 8 hyperlinks).
//   - Kill ring (Emacs-style with prepend / append accumulation) and a
//     generic UndoStack[T].
//   - Fuzzy match + filter (FuzzyMatchString / FuzzyFilter).
//   - Path / slash-command autocomplete providers and a
//     CombinedAutocompleteProvider.
//   - Leaf components: Container, Text, TruncatedText, Spacer, Box (with
//     render cache), Input (grapheme-aware + word nav + kill ring + undo +
//     bracketed paste), SelectList (scroll viewport, wrap-around, two-column
//     layout, fuzzy filter), SettingsList, Loader (goroutine-driven), and
//     CancellableLoader.
//   - ProcessTerminal: a stateless writer for OSC sequences (titles, OSC
//     9;4 progress, cursor visibility, clear) plus Kitty / modifyOtherKeys
//     lifecycle helpers. It does not own stdin or SIGWINCH.
//
// # Compatibility with the upstream TypeScript package
//
// API surface intentionally mirrors @earendil-works/pi-tui where it makes
// sense in Go (KeyID, KeybindingsManager, KillRing, UndoStack, the helpers
// listed above). The TUI/overlay/Renderer pieces of the upstream package
// are intentionally omitted — see docs/TUI_DESIGN.md for the rationale.
package tui
