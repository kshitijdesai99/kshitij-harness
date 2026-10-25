# Authentication and connection recovery

Keep transient authentication and transport failures from unnecessarily ending work.

Introduced **2026-09-28** · commit `84d64c8` (login and token refresh; connection retries followed).

## Current behavior

`kh login codex` authenticates the only implemented backend. Token refresh, expired-token recovery, connection retries, and idle handling run automatically where applicable. Recovery is bounded and can fail; it is not a guarantee of uninterrupted service.

Codex retries transport failures, selected HTTP statuses (408, 429, 500, 502, 503, 504), and disconnected streams only when no reply text or server-side action has been displayed. Connection recovery has a one-minute budget after the first recoverable failure; cancellation stops it sooner. Idle handling bounds silence, not total response time. Recognized expired-token responses allow one credential-refresh retry; ordinary authentication failures, explicit failed/incomplete responses, and malformed protocol events are not universally retried.

Incomplete failed/canceled output follows the [provider state lifecycle](provider-state.md#state-lifecycle): supplied user inputs and tool results remain, while the partial reply and unpaired calls are discarded. Displayed partial text may still flush despite history rollback. The diagram and checkpoint/replay explanation live with that lifecycle, separately from durable memory.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added Codex login and locked token refresh. | `84d64c8` |
| 2026-09-29 | Added retries for dropped connections before a reply. | `595802d` |
| 2026-10-04 | Expanded connection recovery and idle handling. | `c8de1bf` |
| 2026-10-04 | Consolidated authentication and retry code inside the Codex adapter. | `17cb8d1` |
| 2026-10-16 | Added recovery from expired Codex tokens. | `cf2024c` |

## Inspect

- [Retry handling](../internal/provider/codex/retry.go) and [authentication](../internal/provider/codex/auth.go)
- [Retry tests](../internal/provider/codex/test/retry_test.go) and [authentication tests](../internal/provider/codex/test/auth_test.go)
- [Provider-owned history](architecture.md#provider-owned-history)

[Feature index](README.md)
