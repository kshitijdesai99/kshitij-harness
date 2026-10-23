# Architecture

How kh separates orchestration, model state, tools, and terminal presentation.

## Application and turn loop

The [CLI](../cmd/kh/main.go) selects configuration, opens the session and memory store, and assembles tools. The [chat lifecycle](../cmd/kh/chat.go) manages retrieval, learning hooks, saving, and user controls.

The [agent runner](../internal/agent/loop.go) needs only a provider’s `Step` method:

1. Process the user message, including an attached image, then run the pre-message hook.
2. Ask the provider for a reply or tool calls.
3. Execute a batch of tool calls concurrently; return results in the original call order.
4. Repeat until the provider finishes without calls.

The hook runs for initial, steering, and queued user messages, not empty tool-result steps. This keeps retrieval attached to the actual question. Concurrent tools share the workspace: call ordering is not a dependency scheduler.

## Provider-owned history

The [provider contract](../internal/provider/provider.go) owns opaque save/load state, replay, model settings, and usage metrics. Protocol details and credentials stay in the adapter rather than the runner. The [backend selector](../internal/backend/backend.go) currently implements only Codex; the interface is an extension point, not a claim of multiple working providers.

Failed or cancelled provider steps discard partial replies and unpaired calls while preserving supplied input and results. Steering waits for that cleanup before continuing. Completed tool outputs remain paired with their calls, including when a later hook fails.

## Presentation and context

The [terminal layer](../internal/terminal/markdown.go) buffers a completed reply as one Markdown document so lists and links survive streaming boundaries. It flushes on completion, failure, or cancellation. Explicit styles avoid terminal-background queries that could consume input.

An optional [repository map](../internal/repomap/repomap.go) can supply initial project context. It is off by default (`map_cap: 0`); the agent inspects files on demand instead of enlarging the system prompt. Durable memory is retrieved separately and bounded before injection; full provenance and procedures are loaded on demand. Conversation history remains separate from the memory database.

## Design boundaries

- **Runner:** model/tool orchestration, independent of a concrete backend.
- **Provider:** protocol, resumable conversation state, and model capabilities.
- **Application:** session lifecycle, policy, memory hooks, and delegation wiring.
- **Tools:** filesystem/process access and approval enforcement.
- **Memory store:** versioned knowledge and inert procedure payloads, not execution authority.

See [delegation](delegation.md), [memory](memory.md), and [session safety](safety-and-sessions.md) for the operating consequences.
