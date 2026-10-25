# Version history and provenance

Explain how retained knowledge changed without silently replacing earlier versions.

Introduced **2026-10-05** · commit `0a203a6` (append-only history).

## Current behavior

- `kh memory get ID` inspects a version; `kh memory history repo KEY` inspects a topic’s history. The model tool also exposes get/history.
- Notes retain discussion context, statement/save timestamps, and replacement links. Expected-version and statement-time checks reject stale writes.
- The newest version of a scoped topic is current. Forgetting preserves earlier versions; history explains saved knowledge, not every attempted extraction or all conversation activity.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Added append-only versions, history inspection, and expected-version checks. | `0a203a6` |
| 2026-10-16 | Added discussion context, statement timestamps, replacement links, and richer history filters. | `46c225b` |
| 2026-10-17 | Guarded background updates against misapplied topic changes. | `77639f2` |

## Inspect

[Store](../internal/memory/memory.go) · [Provenance tests](../internal/memory/test/provenance_test.go) · [Database design](../manual/memory.md#database-design)

[Feature index](README.md)
