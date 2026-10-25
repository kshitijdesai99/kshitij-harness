# Model/effort controls

Tune the model and reasoning effort for subsequent conversation turns.

Introduced 2026-09-28 · commit `84d64c8` (model flag; effort and in-chat controls came later).

## Current behavior

Use `/model` and `/effort` to inspect or change settings. Preferences persist in configuration; open chats follow changed preferences on later turns, rather than changing an in-progress reply. Only the Codex backend is implemented; model selection is not a choice among multiple implemented providers.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Introduced model selection through a CLI flag. | `84d64c8` |
| 2026-09-28 | Added configured reasoning effort and the `-effort` flag alongside model selection. | `4f09a5a` |
| 2026-09-28 | Added `/model` and `/effort` for switching mid-chat from the next message while keeping history. | `be53ef2` |
| 2026-09-29 | Persisted changes and made other open chats follow them on later messages. | `595802d` |
| 2026-10-01 | Added a backend-neutral provider contract and selector, with Codex implemented. | `60e2bd1` |

## Inspect

- [Commands](../cmd/kh/main.go), [configuration](../internal/config/config.go), and [provider tests](../cmd/kh/test/provider_test.go).
- [Essential controls](../README.md#use) and [provider architecture](../manual/architecture.md#provider-owned-history).

[Feature index](README.md)
