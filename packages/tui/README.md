# packages/tui

Go port of [`@earendil-works/pi-tui`](https://www.npmjs.com/package/@earendil-works/pi-tui), the small terminal-UI library underlying pi-go's coding agent.

> **Position in the architecture.** pi-go's interactive coding-agent UI is driven by [`charm.land/bubbletea/v2`](https://charm.land). This package does **not** provide a primary event loop, raw-mode stdin reader, differential renderer, or overlay manager. It exposes framework-agnostic helpers that compose well with Bubble Tea, lipgloss, or any other renderer. See [`docs/TUI_DESIGN.md`](../../docs/TUI_DESIGN.md) for the rewrite rationale.

## Decision record (route A)

> **"Kept" means ported into this library, not wired into production.** Most kept
> symbols are available for embedders but are not yet consumed by the interactive
> coding-agent UI. The exact set production code (cmd/ + packages/coding-agent) is
> allowed to import is the `wiredTUIComponents` allowlist in
> [`scripts/check_arch.go`](../../scripts/check_arch.go); everything else is
> "ported but not wired" under route A. See [`docs/TUI_DESIGN.md`](../../docs/TUI_DESIGN.md)
> and `docs/TS_COMPATIBILITY.md` for the wiring status.

| | Upstream TS | This package |
|---|---|---|
| Event loop | `Terminal.start(onInput, onResize)` + raw-mode stdin + Kitty negotiation | **Removed.** ProcessTerminal is output-only. Embedders use Bubble Tea. |
| Differential renderer | `TUI.doRender` — full diff + overlay compositor | **Removed.** Use Bubble Tea / lipgloss. |
| Overlay stack | `TUI.showOverlay`, focus stack, `nonCapturing` | **Removed.** Use Bubble Tea overlays / panes. |
| Tree composition | `Container` | Kept — pure structural composition primitive. |
| Keyboard parsing | `keys.ts` (1400 LoC) | Kept — `keys.go`, `keys_kitty.go`, `keys_modify_other.go`. |
| StdinBuffer | `stdin-buffer.ts` (434 LoC) | Kept — `stdin_buffer.go` with full CSI / OSC / DCS / APC / SS3 / SGR mouse / bracketed-paste handling. |
| Width utilities | `utils.ts` (1148 LoC) | Kept — grapheme-cluster aware via `rivo/uniseg`. |
| Markdown | `components/markdown.ts` (814 LoC, marked-based) | Replaced — `goldmark` + custom ANSI renderer (GFM: table, strikethrough, autolink, tasklist). |
| Components | Box / Text / TruncatedText / Spacer / Input / SelectList / SettingsList / Loader / CancellableLoader / Image / Markdown | Kept (Editor was deleted; use Bubble Tea's `bubbles/textarea` for multi-line). `BaseComponent` removed in favour of optional-interface dispatch. |

## Layout

```
component.go        — Component / InputHandler / Invalidator / Focusable / WantsKeyRelease 接口；Container；CursorMarker
text.go             — plain Text
truncated_text.go   — single-line ellipsis text
spacer.go           — vertical Spacer
box.go              — padded container with bg + render cache
input.go            — grapheme-aware single-line Input (word nav, undo, kill ring, bracketed paste)
select_list.go      — scrolling list with wrap, fuzzy-prefix filter, two-column layout
settings_list.go    — toggle list with theme/scroll/description column
loader.go           — goroutine-driven spinner
cancellable_loader.go — Loader + Esc/Ctrl+C
image.go            — inline image component (ported; not yet wired into the coding-agent transcript)
markdown.go         — goldmark-driven ANSI markdown renderer (GFM: table, strike, autolink, tasklist)

terminal.go         — ProcessTerminal: stateless writer + cursor / progress / title helpers
terminal_image.go   — Capability detection (Apple Terminal, Hyper, Konsole, JediTerm, …),
                      image protocol encoding (Kitty + iTerm2), image dimension parsing

keys.go, keys_kitty.go, keys_modify_other.go
                    — KeyId, Key constants, ParseKey, MatchesKey, IsKeyRelease/Repeat,
                      DecodeKittyPrintable / DecodePrintableKey
keybindings.go      — KeybindingsManager + TUIKeybindings + conflict detection
native_modifiers.go — Pluggable native-modifier helper for darwin Shift detection (RWMutex-protected)

stdin_buffer.go     — Sequence buffering + bracketed paste + Kitty CSI-u dedup
kill_ring.go        — Emacs kill ring (Push / Peek (string,bool) / Rotate, prepend & accumulate)
undo_stack.go       — UndoStack[T any]

word_navigation.go  — FindWordBackward / FindWordForward + WordNavigationOptions{Segment, IsAtomic}
utils.go            — VisibleWidth, TruncateToWidth, WrapTextWithANSI, SliceByColumn /
                      SliceWithWidth, ExtractSegments, ApplyBackgroundToLine, IsWhitespaceChar,
                      IsPunctuationChar, PunctuationCharset, PunctuationRegex, NormalizeTerminalOutput
ansi.go             — ansiSequenceLength, ExtractAnsiCode, StripAnsi
fuzzy.go            — FuzzyMatchScore / FuzzyMatchString / FuzzyFilter
autocomplete.go     — AutocompleteItem / Provider, SlashCommandAutocompleteProvider,
                      PathAutocompleteProvider (fd-first + ReadDir fallback, quoted/@-prefix
                      tokenisation), CombinedAutocompleteProvider
editor_component.go — EditorComponent interface (kept for downstream type assertions)
```

Each file has matching `*_test.go` coverage. Run `go test -race ./packages/tui/...`.

## Future work

- Wire `WordNavigationOptions{Segment, IsAtomic}` into the Input so paste markers
  and emoji ZWJ sequences move as a single unit.
- Add a multi-line editor (when there's a concrete in-tree consumer that
  isn't already using `charm.land/bubbles/v2/textarea`).
- Optionally add a `keys_test.go` parametric harness driven by an upstream
  fixture file once the TS package ships one.
