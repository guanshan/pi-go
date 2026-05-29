// Package extensions defines the runtime contract for coding-agent extensions:
// the event bus, lifecycle event types, tool and slash-command definitions, and
// the Runner that dispatches session events to registered extension handlers.
// Compile-time Go extension factories implement this contract; the host wires
// them into a session through the Runner.
package extensions
