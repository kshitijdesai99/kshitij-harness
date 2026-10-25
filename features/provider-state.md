# Provider separation and resumable state

Keep model protocols and conversation history out of the orchestration loop.

Introduced **2026-09-28** · commit `84d64c8` (provider-owned history contract; persistence followed that day).

## Current behavior

The backend-neutral provider contract owns stepping, saved state, loading, and replay; only Codex is implemented. Failed or canceled partial steps discard incomplete replies and calls while preserving supplied user inputs and tool results. Saved conversations resume provider history, separately from durable memory. This separation is not a claim of multiple working model backends or lossless context compaction.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial provider contract kept wire history inside the backend. | `84d64c8` |
| 2026-09-28 | Added save/load and resumable chat. | `3354dc0` |
| 2026-09-28 | Added replay of earlier conversation on resume. | `3e21914` |
| 2026-10-01 | Decoupled orchestration and hardened incomplete-step handling. | `60e2bd1` |
| 2026-10-04 | Consolidated the Codex adapter and backend selection. | `17cb8d1` |

## Inspect

- [Provider contract](../internal/provider/provider.go) and [Codex stream handling](../internal/provider/codex/stream.go)
- [Stream tests](../internal/provider/codex/test/stream_test.go)
- [Provider-owned history](../manual/architecture.md#provider-owned-history) and [saved sessions](../manual/safety-and-sessions.md#saved-history-versus-running-chats)

[Feature index](README.md)
