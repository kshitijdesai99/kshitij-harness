# Bounded keyword recall

Supply relevant stored knowledge before answering without requiring local model downloads.

Introduced **2026-09-29** · commit `14dd3e6`.

## Current behavior

Keyword-only retrieval is the default and requires no model downloads. After each actual user query arrives, including steering and queued messages, and before the provider answers, recall considers repo and global memory within `memory_max_tokens` (estimated as characters/4). Compact notes enter context; [history](memory-history.md) and [full procedures](procedure-loading.md) remain available on demand. See [learning hooks](learning-hooks.md) for timing relative to saving.

Common standalone greetings, check-ins, acknowledgments, and knowledge already present in chat avoid unnecessary topic retrieval. Corrections, required summary notes, and explicitly named references still load. Selection favors applicable notes with [provenance and replacement relationships](memory-history.md), not the volume of saved knowledge. Retrieval can miss or fail; relevance and completeness are not guaranteed. [Optional hybrid reranking](hybrid-retrieval.md) can reject weak matches rather than filling a quota. Use [diagnostics](retrieval-diagnostics.md) to inspect selection; current user requests override retrieved reference data.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Introduced keyword discovery with a small matching-detail and character cap before each answer. | `14dd3e6` |
| 2026-10-05 | Replaced discovery records with append-only notes, summary-aware recall, and conversation-based seen tracking. | `0a203a6` |
| 2026-10-16 | Added broader knowledge selection, a total context budget, and recall for actual steering/queued messages. | `46c225b` |
| 2026-10-23 | Added compact procedure hints and explicit reference matching. | `f802b27` |
| 2026-10-23 | Improved applicability selection, then skipped unnecessary topic retrieval. | `24e64b7`, `13e26fd` |

## Inspect

- Source: [recall](../internal/memory/recall.go), [keyword selection](../internal/memory/search.go).
- Tests: [selection](../internal/memory/test/selection_test.go), [recall efficiency](../internal/memory/test/recall_efficiency_test.go), [reference recall](../internal/memory/test/reference_recall_test.go).

[Feature index](README.md)
