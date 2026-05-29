# packages/coding-agent

Go port of `@earendil-works/pi-coding-agent`.

Implemented:

- CLI entrypoint through `Main`
- argument parser and help
- session manager wrappers
- SDK-style `CreateAgentSession`
- built-in coding tool factories
- print, JSON, RPC, and lightweight interactive mode helpers
- resource loading, package commands, and HTML export wrappers
- minimal Node JSONL bridge for simple `.ts`/`.js`/`.mjs` script extensions

## Limitations

- **User script extensions are partial.** The Go port can load simple
  `.ts`/`.js`/`.mjs` extensions through the Node JSONL bridge, register/execute
  custom tools, declare CLI flags, subscribe to events, mutate/cancel
  before-hooks, and dispatch basic slash commands. It is not full TypeScript
  ExtensionAPI parity yet: rich `ctx.ui`, message renderers, provider/model
  registration, settings, and complex examples are still porting targets. See
  [docs/EXTENSIONS_DESIGN.md](../../docs/EXTENSIONS_DESIGN.md).
