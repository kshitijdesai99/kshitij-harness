# Retrieval explanations

Inspect why memory was selected, rejected, skipped, or replaced by fallback.

Introduced **2026-10-23** · commit `24e64b7`.

## Current behavior

Use `kh memory status` to inspect configuration/runtime status and `kh memory explain "QUERY"` to examine a query's retrieval, including [hybrid fallback](hybrid-retrieval.md). In chat, `/memory explain` displays the last recall's IDs and scores without searching again. Use [memory controls](memory-controls.md) to inspect stored notes themselves rather than the selection trace.

Diagnostics expose stage counts, candidate/loaded IDs, scores, rejection details, timing, and fallback or skip reasons. Per-message summaries are bounded; the detailed view retains more evidence. A successful pipeline stage does not prove relevance, and an empty load can reflect either no new applicable knowledge or the context budget. These controls are not a unified event trace or a record of every learning attempt.

## Trace in context

The [retrieval flow](keyword-recall.md#retrieval-flow) separates required recall, skipped topic search, model-stage fallback, and zero relevant topic results. The trace records the actual recall, including loaded IDs outside topic search; selected candidates are not necessarily injected when seen tracking or the total budget excludes them. In-chat explain reuses that trace rather than running retrieval again.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Added retrieval trace formatting and explain controls alongside relevance improvements. | `24e64b7` |
| 2026-10-23 | Added explicit skip reasons, bounded per-message summaries, and clearer helper/fallback reporting. | `13e26fd` |

## Inspect

- Source: [diagnostic formatting](../internal/memory/diagnostic.go), [recall](../internal/memory/recall.go), [CLI](../cmd/kh/memory.go), [chat](../cmd/kh/chat.go).
- Tests: [diagnostics](../internal/memory/test/diagnostic_test.go), [recall efficiency](../internal/memory/test/recall_efficiency_test.go).

[Feature index](README.md)
