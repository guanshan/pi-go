# packages/ai

Go port of `@earendil-works/pi-ai`, aligned against the TypeScript package at
upstream `0.76.0`.

Implemented:

- shared content, message, usage, model, and tool-call types
- model registry and auth storage wrappers
- thinking-level helpers
- unified chat provider registry and dispatch for `Complete`, `Stream`, `CompleteSimple`, and `StreamSimple`
- OAuth model modifiers during registry construction, including GitHub Copilot enterprise base URLs
- independent image generation options, provider registry, and image model catalog
- OpenAI-compatible, Anthropic, Google, Bedrock, Mistral, and faux providers through the shared runtime

Known gaps:

- The Go validation layer implements the TypeScript package's observable JSON-schema coercion/error behavior for common tool schemas, but it is not a full TypeBox compiler clone.
- Provider coverage is still narrower than the TypeScript package; newly added upstream providers should be ported through the shared registry rather than separate dispatch paths.
