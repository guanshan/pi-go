# DOOM Overlay Demo

Play DOOM as an overlay in pi. Demonstrates that the overlay system can handle real-time game rendering at 35 FPS.

> **License & build note.** This demo embeds DOOM, which derives from
> [doomgeneric](https://github.com/ozkl/doomgeneric) and id Software's DOOM
> source — both **GPL-2.0**. The compiled `doom/build/doom.js` and
> `doom/build/doom.wasm` are therefore **not** committed to this MIT-licensed
> repository; build them locally with `doom/build.sh` (requires Emscripten).
> The resulting binaries are GPL-2.0, separate from pi-go's MIT license.
>
> This TypeScript example is also reference-only for pi-go. The Go port can
> execute simple `.ts`/`.js` custom-tool and slash-command extensions through
> its minimal Node JSONL bridge, but this demo depends on rich overlay UI from
> the upstream TypeScript ExtensionAPI, which is not implemented yet (see
> [`docs/EXTENSIONS_DESIGN.md`](../../../docs/EXTENSIONS_DESIGN.md)).

## Usage

> Requires the upstream TypeScript Pi runtime. Build the WASM first:
>
> ```bash
> ./doom/build.sh   # clones doomgeneric and compiles doom.js/doom.wasm
> ```

```bash
pi --extension ./examples/extensions/doom-overlay
```

Then run:
```
/doom-overlay
```

The shareware WAD file (~4MB) is auto-downloaded on first run.

## Controls

| Action | Keys |
|--------|------|
| Move | WASD or Arrow Keys |
| Run | Shift + WASD |
| Fire | F or Ctrl |
| Use/Open | Space |
| Weapons | 1-7 |
| Map | Tab |
| Menu | Escape |
| Pause/Quit | Q |

## How It Works

DOOM runs as WebAssembly compiled from [doomgeneric](https://github.com/ozkl/doomgeneric). Each frame is rendered using half-block characters (▀) with 24-bit color, where the top pixel is the foreground color and the bottom pixel is the background color.

The overlay uses:
- `width: "90%"` - 90% of terminal width
- `maxHeight: "80%"` - Maximum 80% of terminal height
- `anchor: "center"` - Centered in terminal

Height is calculated from width to maintain DOOM's 3.2:1 aspect ratio (accounting for half-block rendering).

## Credits

- [id Software](https://github.com/id-Software/DOOM) for the original DOOM
- [doomgeneric](https://github.com/ozkl/doomgeneric) for the portable DOOM implementation
- [pi-doom](https://github.com/badlogic/pi-doom) for the original pi integration
