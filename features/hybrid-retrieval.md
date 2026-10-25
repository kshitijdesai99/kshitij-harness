# Local hybrid retrieval

Find related lessons beyond exact keywords and rerank them for applicability.

Introduced **2026-10-16** · commit `46c225b` (semantic/keyword hybrid support). Managed Qwen setup and reranking followed on **2026-10-23**, commit `6fc58c6`.

## Current behavior

Hybrid retrieval is opt-in through `/memory hybrid on`; keyword-only retrieval remains the default. Explicit setup downloads and verifies Qwen3-Embedding 0.6B and Qwen3-Reranker 0.6B, requiring Ollama and Python 3.10+ with venv support. Startup uses cached models only. `/memory hybrid off` restores keywords without deleting models or vectors; open chats apply changes on their next message.

Vectors fill lazily with bounded backfill, not continuous indexing. Applicability filtering can return no relevant notes. Embedding/reranking failures fall back to keywords with stage-specific diagnostics. The [managed helper](hybrid-runtime.md) documents contention limits and revision/upgrade policy; overrides and setup details belong in the manual.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-16 | Introduced embeddings, lazy vector caching, and keyword/semantic hybrid search with fallback. | `46c225b` |
| 2026-10-23 | Added opt-in managed Qwen embedding/reranking setup and chat controls. | `6fc58c6` |
| 2026-10-23 | Bounded local contention/timeouts and corrected worker bursts. | `d49942b`, `5581835` |
| 2026-10-23 | Improved applicability filtering and exposed retrieval diagnostics. | `24e64b7` |
| 2026-10-23 | Fenced helper revisions and avoided unnecessary retrieval. | `13e26fd` |

## Inspect

- Source: [semantic search](../internal/memory/semantic.go), [ranking](../internal/memory/search.go), [managed runtime](../internal/hybrid/runtime.go).
- Tests: [hybrid search](../internal/memory/test/hybrid_test.go), [relevance](../internal/memory/test/relevance_test.go), [runtime](../internal/hybrid/test/runtime_test.go).
- Manual: [optional local hybrid search](../manual/memory.md#optional-local-hybrid-search).

[Feature index](README.md)
