// Package imageresize shrinks inline images to fit the provider request-size
// limits, mirroring the upstream coding-agent image-resize behaviour with a
// stdlib-only implementation (PNG/JPEG/GIF decode, area-averaging downscale,
// PNG/JPEG re-encode). It is depended on by both the @file CLI path and the read
// tool, and intentionally lives in its own leaf package so packages/ai stays at
// the bottom of the dependency DAG.
package imageresize
