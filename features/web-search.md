# Web search

Research online through the provider without local scraping.

Introduced 2026-09-28 · commit `e192d21`.

## Current behavior

Codex hosted search is available by default through `web_search`, but the CLI defers its schema until needed. Discover and describe `enable_web_search` through [`tool_registry`](project-configuration.md#compact-context-and-tool-discovery), then invoke it through `tool_invoke` with `arguments: {}`. Activation is idempotent and performs no search or network request itself; hosted search, open-page, and find-text actions become available on the next provider step and are used directly, not through `tool_invoke`. With `web_search: false`, the activation tool is not registered.

Search runs provider-side; this is not a local browser or scraping tool. Fresh non-web requests avoid the hosted schema, without eagerly exposing other tool schemas or capability catalogs.

## History and compaction

Activation alone is not saved: resumed or forked sessions with no hosted-call history start lean and can activate again. Existing hosted-call history still needs the hosted schema for provider validation. When configuration permits search, loaded history retains that schema even before a new activation. With search disabled, an ordinary step containing hosted-call history fails explicitly and asks for `/compact`, rather than silently enabling search or dropping history.

[Compaction](compaction.md) retains only the minimal hosted schema when that history requires it and forces `tool_choice: "none"`; it cannot start a new search. Successful compaction removes the old hosted-call items from working context.

## Provider API

The CLI uses `provider.OnDemandWebSearch` with `DeferWebSearch` and `EnableWebSearch`. Direct callers of the existing Codex `New` constructor retain eager behavior when configured with web search enabled. Effective tools are snapshotted consistently for request construction and the corresponding cache key.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Enabled Codex server-side search by default via configuration. | `e192d21` |
| 2026-09-28 | Displayed open-page and find-text actions in live output and replay, not just search queries. | `1bcbeb9` |
| 2026-09-29 | Updated agent guidance to prefer built-in search over curl. | `2660dc3` |
| 2026-11-03 | Deferred hosted schemas in the CLI until explicit activation, preserving historical-call validation and tool-less compaction. | Current change |

## Inspect

- [Activation tool](../internal/tools/web_search.go), [CLI wiring](../cmd/kh/main.go), [provider contract](../internal/provider/provider.go), and [Codex client](../internal/provider/codex/client.go).
- [Configuration](../internal/config/config.go).
- [Hosted-history compaction](../internal/provider/codex/compact.go) and [representative coverage](../internal/provider/codex/test/compact_search_test.go).
- [Provider architecture](architecture.md#provider-owned-history) and [delegation/tool boundaries](native-agents.md).

[Feature index](README.md)
