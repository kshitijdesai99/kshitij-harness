# Inspection and forgetting

Inspect retained knowledge and stop using unwanted topics explicitly.

Introduced **2026-09-29** · commit `14dd3e6` (search/get/forget).

## Current behavior

- CLI and model tool provide search, get, and history; `kh memory forget repo KEY` or an explicitly requested tool forget retires a scoped topic.
- Search includes repo/global knowledge by default; global-only inspection is available. Ordering, bounded results, and inclusive RFC3339 time filters constrain inspection. History and forgetting use an exact scope.
- Forgetting appends a tombstone while preserving [history](memory-history.md); purging deletes the topic's versions and associated payloads. The model tool cannot save notes directly; [learning hooks](learning-hooks.md) handle automatic note saving.
- Set `"memory_model": "off"` in configuration to disable automatic saving while retaining retrieval. Use [retrieval diagnostics](retrieval-diagnostics.md) for status/explanations and [procedure loading](procedure-loading.md) for the tool's `load` action.

```bash
kh memory search "topic"
kh memory get ID
kh memory history repo KEY
kh memory forget repo KEY
```

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added CLI/tool inspection and explicit forgetting. | `14dd3e6` |
| 2026-10-05 | Added history and changed forgetting to preserve append-only versions. | `0a203a6` |
| 2026-10-05 | Made background hooks the only memory writers. | `8ab0023` |
| 2026-10-16 | Expanded scoped provenance inspection, ordering, and time filters. | `46c225b` |
| 2026-10-23 | Added on-demand reference loading. | `f802b27` |

## Inspect

[Memory tool](../internal/tools/memory.go) · [CLI](../cmd/kh/memory.go) · [Tool tests](../internal/tools/test/memory_provenance_test.go)

[Feature index](README.md)
