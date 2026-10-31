# Architecture

Understand how kh connects model orchestration, tools, conversation state, and learning. Operating instructions and history live on each feature's owning page.

## Source map

Use this map to find the owning package; linked feature pages explain behavior and history.

| Package / source | Responsibility | Feature guide |
|---|---|---|
| [`cmd/kh`](../cmd/kh/) | CLI entry points, chat lifecycle, and application wiring | [Chat modes](chat-modes.md), [native agents](native-agents.md) |
| [`internal/agent`](../internal/agent/) | Model/tool turn orchestration | [Execution loop](execution.md) |
| [`internal/backend`](../internal/backend/) | Provider construction and selection | [Provider state](provider-state.md) |
| [`internal/config`](../internal/config/) | Defaults and persisted configuration | [Model settings](model-settings.md) |
| [`internal/ecosystem`](../internal/ecosystem/) | Shared agent directory, transactional admission and process lifecycle | [Native agents](native-agents.md), [worker lifecycle](worker-lifecycle.md) |
| [`internal/help`](../internal/help/) | Standalone help requests | [Help](help.md) |
| [`internal/hybrid`](../internal/hybrid/) | Managed local retrieval services | [Hybrid runtime](hybrid-runtime.md) |
| [`internal/image`](../internal/image/) | Image attachment reading and clipboard snapshots | [Images](images.md) |
| [`internal/learn`](../internal/learn/) | Background extraction and learning safeguards | [Learning queue](learning-queue.md), [origin protections](learning-origin.md) |
| [`internal/memory`](../internal/memory/) | Versioned storage, retrieval, and reference bundles | [Knowledge store](knowledge-store.md), [hybrid retrieval](hybrid-retrieval.md), [portability](procedure-portability.md) |
| [`internal/mcp`](../internal/mcp/) | Trusted external MCP connections and protocol calls | [Project configuration](project-configuration.md) |
| [`internal/projectconfig`](../internal/projectconfig/) | Filesystem configuration discovery and capability references | [Project configuration](project-configuration.md) |
| [`internal/provider`](../internal/provider/) | Provider contract and protocol adapters | [Provider state](provider-state.md), [connection recovery](connection-recovery.md) |
| [`internal/repomap`](../internal/repomap/) | Repository context generation | [Repository map](repository-map.md) |
| [`internal/session`](../internal/session/) | Saved session files and state | [Saved sessions](saved-sessions.md) |
| [`internal/terminal`](../internal/terminal/) | Input, rendering, and terminal feedback | [Input](input.md), [Markdown](markdown.md), [feedback](terminal-feedback.md) |
| [`internal/tools`](../internal/tools/) | Tool implementations, approvals, and filesystem safety | [Shell](shell.md), [editing](editing.md), [write approvals](write-approvals.md) |

## Application and turn loop

The [CLI](../cmd/kh/main.go) selects configuration, opens stores, and assembles tools. The [chat lifecycle](../cmd/kh/chat.go) manages retrieval, learning, persistence, and controls. The [runner](../internal/agent/loop.go) needs only the provider's `Step` method:

![Chat and memory flow: recall for the answer, selective learning in the background](../diagrams/memory-architecture.svg)

[Memory overview SVG](../diagrams/memory-architecture.svg): recall supplies the answer path; selective learning saves useful knowledge in the background. See [knowledge storage](knowledge-store.md) for database details.

The [execution loop](execution.md) runs tool batches concurrently and returns results in call order. Shared files require explicit dependency coordination. See [learning hooks](learning-hooks.md) for message timing and [native agents](native-agents.md) for delegation.

## Agent ecosystem

![Recursive delegation with a shared registry and one capacity budget](../diagrams/agent-coordination.svg)

[Coordination SVG](../diagrams/agent-coordination.svg): the root remains manager; descendants default to worker and may promote themselves to delegate. Parent links record ownership, not communication permissions. All participants discover and coordinate within the same ecosystem; one shared live-agent budget prevents recursive capacity multiplication. [Native agents](native-agents.md) owns the tool contract; [worker lifecycle](worker-lifecycle.md) owns capacity, stop and recovery behavior.

The SQLite registry at `~/.kh/ecosystem.db` stores shared coordination state. The root initializes and refreshes the configured limit on genuine user turns and resume; descendants and peer messages cannot override it. Lower limits block new admission without killing existing agents.

Each agent retains its own provider conversation. Shared first-line task summaries (at most 280 runes) and bounded conclusions do not expose raw multiline task prompts or full histories. Project files and memory remain shared resources, so file ownership and verification are still explicit responsibilities.

## Provider-owned history

The [provider contract](../internal/provider/provider.go) owns protocol state, save/load, replay, settings, and usage metrics. The [backend selector](../internal/backend/backend.go) currently implements only Codex; an interface is not multiple working providers.

[Provider state](provider-state.md) defines failed-step cleanup. [Saved sessions](saved-sessions.md) persist conversations; [steering](steering.md) controls active turns; [compaction](compaction.md) summarizes working context.

## Presentation and context

[Markdown rendering](markdown.md) owns whole-reply presentation; [terminal feedback](terminal-feedback.md) and [turn metrics](turn-metrics.md) expose runtime signals. [Repository maps](repository-map.md) provide optional initial project context. [Keyword recall](keyword-recall.md) and optional [hybrid retrieval](hybrid-retrieval.md) select bounded memory context; details load on demand.

Conversation history, [durable knowledge](knowledge-store.md), and [project configuration](project-configuration.md) are separate. Retrieved notes, project capabilities, and [procedure assets](procedure-loading.md) are reference data, not execution authority. The application owns MCP startup trust and per-call approval; tools and external servers operate subject to OS permissions, without kh filesystem confinement.

## Design boundaries

- **Runner:** model/tool orchestration, independent of a concrete backend.
- **Provider:** protocol, resumable conversation state, and model capabilities.
- **Application:** lifecycle, policy, memory hooks, and delegation wiring.
- **Tools:** unrestricted process/filesystem access subject to OS permissions, [command approval policy](write-approvals.md), and [edit integrity checks](edit-path-safety.md).
- **Memory store:** versioned knowledge and inert procedure payloads, not execution authority.

[Feature index](README.md) · [Development](development.md)
