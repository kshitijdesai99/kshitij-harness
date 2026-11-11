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
kh memory remember repo instruction safety "Keep stored scripts inert." --importance critical
kh memory remember repo fact deadline "Review ends at the agreed time." --expires 2026-12-01T17:00:00Z
```

`remember` accepts `--importance normal|critical` and `--expires RFC3339|none` before or after positional arguments. Omitted flags preserve an existing topic's tier and expiry; `--importance normal` demotes it, and `--expires none` clears its expiry explicitly. Dates require an explicit timezone; kh never guesses a relative date. Use `--` before literal positional content beginning with `--`. Importance is retrieval metadata, not higher instruction authority. Expiry stops routine recall at that time without deleting history. Get/history and compact note output show tier, expiry, and expired state; an empty legacy tier is labeled unclassified. Automatic classification and freshness are described in [learning and summaries](learning-queue.md).

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added CLI/tool inspection and explicit forgetting. | `14dd3e6` |
| 2026-10-05 | Added history and changed forgetting to preserve append-only versions. | `0a203a6` |
| 2026-10-05 | Made background hooks the only memory writers. | `8ab0023` |
| 2026-10-16 | Expanded scoped provenance inspection, ordering, and time filters. | `46c225b` |
| 2026-10-23 | Added on-demand reference loading. | `f802b27` |

## Inspect

[Memory tool](../internal/tools/memory.go) · [CLI](../cmd/kh/memory.go) · [Tool tests](../internal/tools/test/memory_provenance_test.go) · [CLI metadata tests](../cmd/kh/test/memory_test.go)

[Feature index](README.md)


## 2026-11-11 — Versioned importance and explicit expiry controls

- Status: implemented
- Rationale: Expose normal/critical retrieval metadata and absolute expiry through existing remember/get/history paths rather than adding a native save action. Omitted flags preserve existing metadata; normal explicitly demotes and --expires none explicitly clears. RFC3339 dates require a timezone and errors do not echo invalid date content. Expiry suppresses routine recall without deleting history; importance never grants instruction authority.
- Affected: cmd/kh/memory.go; internal/tools/memory.go; metadata/provenance tests; memory-controls.md and memory-history.md
- Verification / outstanding: Metadata preservation, promotion/demotion, expiry/clear, global scope, literal flags, timezone normalization and error-privacy tests passed, together with the final normal suite and vet. Applicable configuration/tool race checks and the affected CLI hybrid race case passed; see [combined verification and exact race scope](keyword-recall.md). Independent review found no remaining demonstrated defects. CLI fixtures isolate HOME/cwd, use non-terminal stdin and set KH_AUTO_REBUILD=0.
