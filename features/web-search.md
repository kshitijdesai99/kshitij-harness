# Web search

Research online through the provider without local scraping.

Introduced 2026-09-28 · commit `e192d21`.

## Current behavior

Codex built-in search is enabled by default through `web_search`. Search runs provider-side; this is not a local browser tool. The local tool runner does not implement the search service.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Enabled Codex server-side search by default via configuration. | `e192d21` |
| 2026-09-28 | Displayed open-page and find-text actions in live output and replay, not just search queries. | `1bcbeb9` |
| 2026-09-29 | Updated agent guidance to prefer built-in search over curl. | `2660dc3` |

## Inspect

- [Codex client](../internal/provider/codex/client.go) and [configuration](../internal/config/config.go).
- [Provider architecture](../manual/architecture.md#provider-owned-history) and [delegation/tool boundaries](../manual/delegation.md).

[Feature index](README.md)
