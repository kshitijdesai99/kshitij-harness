# Local hybrid retrieval

Find related lessons beyond exact keywords and rerank them for applicability.

Introduced **2026-10-16** · commit `46c225b` (semantic/keyword hybrid support). Managed Qwen setup and reranking followed on **2026-10-23**, commit `6fc58c6`.

## Current behavior

Hybrid retrieval is opt-in through `/memory hybrid on`; keyword-only retrieval remains the default. Explicit setup downloads and verifies Qwen3-Embedding 0.6B and Qwen3-Reranker 0.6B, requiring Ollama and Python 3.10+ with venv support. Startup uses cached models only. `/memory hybrid off` restores keywords without deleting models or vectors; open chats apply changes on their next message.

Optional `memory_embedding_url`, `memory_embedding_model`, and `memory_reranker_url` configuration settings override defaults. The vector cache fills lazily during semantic searches, with bounded backfill for missing or changed vectors; it is not a continuous indexer.

Applicability filtering can return no relevant notes rather than filling a quota. Embedding/reranking failures fall back to keywords with stage-specific notices; [retrieval diagnostics](retrieval-diagnostics.md) expose fallback status. Retry `/memory hybrid on` to repeat setup and verification. The [managed helper](hybrid-runtime.md) owns runtime files, contention limits, and revision/upgrade policy.

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

[Feature index](README.md)
