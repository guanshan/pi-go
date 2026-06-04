# TUI design: route A

`packages/tui` is the Go port of [`@earendil-works/pi-tui`](https://www.npmjs.com/package/@earendil-works/pi-tui),
the terminal-UI library underlying the upstream TypeScript Pi coding agent. This
document records the design decision that shapes the Go port.

## Decision

pi-go adopts **route A**: `packages/tui` is *not* the primary interactive
renderer. It does not own a main event loop, raw-mode stdin reader, `SIGWINCH`
handling, a differential renderer, or an overlay stack. The interactive
coding-agent UI is instead driven by
[`charm.land/bubbletea/v2`](https://charm.land) together with `bubbles` and
`lipgloss`.

The legacy upstream `TUI` type and its overlay machinery were therefore
intentionally **not** ported. `packages/tui` is a collection of
framework-agnostic primitives that compose with Bubble Tea (or any other
renderer).

## What `packages/tui` provides

- Grapheme-cluster aware width / truncation / wrapping / slicing utilities
  (`rivo/uniseg`), plus ANSI helpers and segment extraction.
- Word navigation helpers.
- Keyboard input: `KeyID`, key parsing, Kitty CSI-u (disambiguation, event
  types, alternate keys), xterm `modifyOtherKeys`, and a keybindings registry.
- `StdinBuffer`: splits raw stdin into complete escape sequences and
  bracketed-paste payloads (CSI / OSC / DCS / APC / SS3 / SGR mouse).
- Terminal-capability detection and image-protocol encoding (Kitty graphics,
  iTerm2 inline images, OSC 8 hyperlinks).
- Fuzzy matching and autocomplete providers.
- Leaf components: `Container`, `Text`, `TruncatedText`, `Spacer`, `Box`,
  `Input`, `SelectList`, `SettingsList`, `Loader`, `CancellableLoader`,
  `Image`, `Markdown` (`goldmark` + a custom ANSI renderer).

## What the coding agent owns instead

The interactive shell in `packages/coding-agent` owns everything that used to
live in the upstream `TUI`/renderer layer:

- the Bubble Tea program lifecycle and `textarea` / `viewport` composition;
- transcript rendering and streaming assistant updates;
- slash-command routing and completion;
- model / login / session / settings / theme / extension selectors;
- rich message views (tool calls, diffs, summaries, bash output, skills);
- footer state and product-level keybindings.

See [`packages/tui/README.md`](../packages/tui/README.md) for the
component-by-component decision record (what was kept, replaced, or removed),
and [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the overall package layout.

## Current interactive parity status

The Bubble Tea shell works, but several interactions are still text fallbacks
rather than the navigable overlays/dropdowns of the TypeScript TUI. This is the
honest current state (parity review P1-3), pending the generic
selector/dialog/overlay state machine the section above calls for:

| Interaction | Current Go behavior | TS target |
| --- | --- | --- |
| `/model`, `/scoped-models` | prints a list | navigable selector |
| `/settings` | dumps JSON | editable settings list |
| `/resume` | numbered list prompt | navigable session picker |
| `/tree`, `/fork` | text tree / argument flow | interactive tree navigation |
| `/login` | OAuth prompts routed through the input/select overlay | OAuth selector overlay |
| `pi config` | numeric line selection | navigable settings list |
| autocomplete | slash / model / prompt / skill prefix match | + path, `@`-refs, extension providers, navigable dropdown |
| keybindings | a few hardcoded keys (Esc/Ctrl+C, double-Esc) | `KeybindingsManager` + user `keybindings.json` |
| extension `ctx.ui` | degraded stub (see [EXTENSIONS_DESIGN.md](EXTENSIONS_DESIGN.md)) | full overlay-backed UI |

Escape now cancels a running slash/bash command (not just an agent turn) — see
`interactiveModel.handleEscape` — but the rest of the overlay/selector work
remains a dedicated effort.
