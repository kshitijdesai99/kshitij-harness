# Architecture

Understand how kh connects model orchestration, tools, conversation state, and learning. Operating instructions and history live on each feature's owning page.

## Application and turn loop

The [CLI](../cmd/kh/main.go) selects configuration, opens stores, and assembles tools. The [chat lifecycle](../cmd/kh/chat.go) manages retrieval, learning, persistence, and controls. The [runner](../internal/agent/loop.go) needs only the provider's `Step` method:

```text
Submitted user message → Image handling / pre-message hook → Model
                                                             ↕
                                                   Tools / delegated agents
                                                             ↓
                                                   Reply + saved conversation

Eligible user messages + successful response/tool evidence
  → Background learning → Versioned notes / procedures → Future retrieval
```

The [execution loop](execution.md) runs tool batches concurrently and returns results in call order. Shared files require explicit dependency coordination. See [learning hooks](learning-hooks.md) for message timing and [native agents](native-agents.md) for delegation.

## Provider-owned history

The [provider contract](../internal/provider/provider.go) owns protocol state, save/load, replay, settings, and usage metrics. The [backend selector](../internal/backend/backend.go) currently implements only Codex; an interface is not multiple working providers.

[Provider state](provider-state.md) defines failed-step cleanup. [Saved sessions](saved-sessions.md) persist conversations; [steering](steering.md) controls active turns; [compaction](compaction.md) summarizes working context.

## Presentation and context

[Markdown rendering](markdown.md) owns whole-reply presentation; [terminal feedback](terminal-feedback.md) and [turn metrics](turn-metrics.md) expose runtime signals. [Repository maps](repository-map.md) provide optional initial project context. [Keyword recall](keyword-recall.md) and optional [hybrid retrieval](hybrid-retrieval.md) select bounded memory context; details load on demand.

Conversation history, [durable knowledge](knowledge-store.md), and project files are separate. Retrieved notes and [procedure assets](procedure-loading.md) are reference data, not execution authority.

## Design boundaries

- **Runner:** model/tool orchestration, independent of a concrete backend.
- **Provider:** protocol, resumable conversation state, and model capabilities.
- **Application:** lifecycle, policy, memory hooks, and delegation wiring.
- **Tools:** process/filesystem access, [approval policy](write-approvals.md), and [sandbox enforcement](shell-sandbox.md).
- **Memory store:** versioned knowledge and inert procedure payloads, not execution authority.

[Feature index](README.md) · [Development](development.md)
