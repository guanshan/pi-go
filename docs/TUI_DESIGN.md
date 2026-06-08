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

The Bubble Tea shell now wires several formerly reference-only primitives from
`packages/tui` into production: Markdown rendering, visible-width/truncation
helpers, fuzzy matching, SelectList for the model picker, and the keybinding
manager. `scripts/check_arch.go` enforces the exact exported-symbol allowlist
(`wiredTUIComponents`), so wiring another `packages/tui` surface requires a
deliberate code and documentation update.

Several flows still use product-local Bubble Tea views rather than the upstream
`TUI` overlay stack. This is the honest current state:

| Interaction | Current Go behavior | TS target |
| --- | --- | --- |
| `/model` | SelectList-backed navigable overlay (Ctrl+L / bare `/model`) | navigable selector |
| `/scoped-models` | prints scoped model summary | navigable selector |
| `/theme` | SelectList-backed navigable overlay (bare `/theme`); `/theme <name>` applies live + persists | navigable selector |
| `/settings` | navigable editor overlay — theme row opens the theme picker, boolean rows toggle in place. Auto-compaction/auto-retry toggles also apply to the **live session** (`AgentSession.SetAuto*Enabled`) immediately, not just on next launch; persistence failures surface in the status line (bare `/settings`) | full editable settings list |
| `/resume` | navigable session picker overlay (bare `/resume`); `/resume <id>` still switches directly | navigable session picker |
| `/tree` | navigable picker over the **full session tree** (`EntriesSnapshot()` — every entry/branch, not just current-branch fork points; current leaf tagged). Selecting the current leaf is a no-op (*Already at this point*); selecting another entry offers a branch-summary choice (No summary / Summarize / Summarize with custom prompt, unless `branchSummary.skipPrompt`) threaded into `NavigateTreeOptions`. Flat `SelectList`, not the TS ASCII tree art/filter modes (bare `/tree`); `<id>` arg still routes directly | interactive tree navigation |
| `/fork` | navigable picker over **all** forkable user messages across every branch (`getEntries()` parity), not just the current branch (bare `/fork`); `<id>` arg still routes directly | interactive fork-point picker |
| `/login` | OAuth prompts routed through the input/select overlay | OAuth selector overlay |
| `pi config` | numeric line selection | navigable settings list |
| autocomplete | slash / model / prompt / skill / path / `@` refs / extension-provider suggestions in a navigable dropdown | TS visual-wrap cursor parity and exact provider ordering |
| keybindings | app command table plus user `keybindings.json`; extension shortcuts are resolved and listed in `/hotkeys` | remaining platform-specific edge cases |
| extension `ctx.ui` | host-backed input/select/confirm/notify overlay bridge (see [EXTENSIONS_DESIGN.md](EXTENSIONS_DESIGN.md)) | full overlay-backed UI and custom renderers |

Escape now cancels a running slash/bash command (not just an agent turn) — see
`interactiveModel.handleEscape`. The model/theme/settings/resume/tree/fork
selectors are navigable overlays (`interactive_command_selectors.go`); `/tree`
and `/fork` read the full session tree and the settings toggles drive the live
session. The remaining dedicated efforts are exact visual-cursor parity, the TS
tree ASCII-art/filter-modes/label-editing affordances (the Go `/tree` is a flat
complete-data `SelectList`), and a fuller `/settings` editor covering more than
the theme + boolean-toggle subset.

Fenced code blocks in assistant markdown are syntax-highlighted via
`alecthomas/chroma` (mirrors TS `highlight.js`); the chroma style is matched to
the resolved theme's brightness (`github-dark` / `github`). See
`packages/tui/markdown_highlight.go` and `MarkdownTheme.SyntaxHighlight`.
