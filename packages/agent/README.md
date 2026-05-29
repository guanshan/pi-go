# packages/agent

Go implementation of the pi agent runtime.

The root package is the loop kernel. It owns `RunAgentLoop`, the lightweight
`Agent` state wrapper, event types, stream/proxy helpers, tool execution, and
small shared types used by callers that want to drive the loop directly.

The `harness` subpackage is the long-running session harness. It owns session
storage, system prompt/resources, compaction, branch summaries, environment
helpers, and persistence-oriented event handling. `AgentHarness` calls
`RunAgentLoop` directly and acts as the reducer for session state.
