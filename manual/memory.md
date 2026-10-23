# Memory and procedures

kh aims to learn from every conversation, selectively retaining useful knowledge rather than turning every exchange into permanent instructions. Reliable, well-referenced, high-quality retrieval is the foundation for useful intelligence, not the volume of saved notes. Learning and retrieval can decline, miss, or fail; neither is exhaustive or guaranteed.

## Database as source of truth

Runtime knowledge lives in SQLite at `~/.kh/memory.db`; saved conversations live separately in `~/.kh/sessions`. The [memory store](../internal/memory/memory.go) versions notes with repo/global scope, discussion context, statement/save timestamps, and links to replaced versions. History preserves provenance rather than silently rewriting a fact.

The [background learner](../internal/learn/learn.go) selectively extracts lasting instructions, established facts, accepted decisions, and gotchas. It uses tool-less model calls and reports saves or failures. Its bounded queue can drop work rather than block chat: automatic learning is selective, not an exhaustive archive or guaranteed save.

Set `"memory_model": "off"` in configuration to disable automatic saving while keeping retrieval available. Inspect saved versions with `get` and `history`; `kh memory forget repo KEY` stops using a topic while preserving its history.

Workers retrieve shared memory but do not learn their delegated task prompts as user instructions. Peer messages are excluded from learning. Current user requests take precedence over retrieved reference data.

## Hook and background-job pipeline

1. **Post-query / pre-answer:** after an actual nonempty query arrives and pasted-image handling succeeds, [`Runner.BeforeMessage`](../internal/agent/loop.go) runs before `provider.Step`. This covers initial, steering, and queued messages, not empty tool-result steps. The [chat callback](../cmd/kh/chat.go) queues `Learner.MessageAt` with the query, recent discussion context, and event time, then retrieves applicable memory into provider context. Queuing extraction does not wait for a save before answering; retrieval itself runs before the provider step.
2. **Post-response:** after a successful, uncanceled run without peer messages, the chat calls `Learner.FinishedAt` with the current query, bounded recent context, observed tool results, reply, and event time. A nonempty reply queues durable-note extraction and, when eligible, procedure extraction; a summary check follows. The summary rebuilds from current notes only when they changed, rather than repeatedly condensing the old summary. Failed or canceled turns do not run this completion hook.
3. **Background execution:** the [learner](../internal/learn/learn.go) runs jobs serially through a bounded 32-job queue, each with a cancellable two-minute context. Full queues drop work with a notice instead of blocking chat. Extraction uses fresh tool-less model calls; bounded evidence and context help interpret a lesson without treating an entire transcript as instructions. Notices are shown between turns. Shutdown allows up to 30 seconds for queued work before cancellation; startup queues a summary catch-up check.

The [startup wiring](../cmd/kh/main.go) creates the learner only in user-facing chats when automatic saving is enabled. Workers still share retrieval, but delegated task prompts are not learned. The learner rejects peer-message inputs, including mixed user/peer composites. If a run includes peer coordination, its tool evidence and reply are excluded from post-learning and recent learning context, even after later user steering. Separate clean user queries still receive the pre-hook. This conservative boundary prevents worker directives from becoming durable user instructions.

## Database design

| Table | Purpose |
|---|---|
| `notes` | Append-only topic versions, scope, provenance, replacement links, and forgotten markers. |
| `notes_fts` | FTS5 keyword index over note keys and text. |
| `notes_vectors` | Optional vector cache keyed by note and model/endpoint identity, with a content hash to detect changes. |
| `reference_payloads` | Full procedures, aliases, inert assets, portable IDs, versions, and import provenance, linked to a note version. |

The current version is the newest row for each `(repo, key)`. Expected-version checks reject stale writes from concurrent agents. Forgetting appends a tombstone and retains history; purging deletes the topic's versions and associated payloads. SQLite WAL supports shared reads while another process writes. See the [store schema](../internal/memory/memory.go), [vector cache](../internal/memory/semantic.go), and [reference transactions](../internal/memory/references.go).

## Retrieval and diagnostics

Keyword-only retrieval is fully supported and works by default without model downloads. After each actual user query arrives, including steering, and before the model answers, the application retrieves relevant knowledge within `memory_max_tokens` (estimated as characters/4). Compact notes enter context; full history and procedures remain on demand.

Good retrieval selects applicable notes, preserves provenance and replacement relationships, and loads supporting detail only when needed. Optional reranking rejects weak matches instead of filling context with more notes. Inspect the [recall pipeline](../internal/memory/recall.go) and [ranking logic](../internal/memory/search.go), or use diagnostics below to examine what was selected; relevance is not guaranteed by a large memory store.

Common standalone greetings, check-ins, and acknowledgments skip topic search, as does knowledge already present in the chat. Corrections, required summary notes, and explicit named references still load. Reranking may correctly return no relevant memories rather than filling a quota.

Useful controls:

```bash
kh memory status
kh memory search "topic"
kh memory get ID
kh memory history repo KEY
kh memory explain "query"
```

In chat, `/memory explain` shows the last retrieval’s IDs and scores without searching again. The model’s [memory tool](../internal/tools/memory.go) supports search, get, history, load, and explicit forget, but not save. Repo-plus-global access, bounded results, and inclusive RFC3339 time filters keep inspection scoped.

## Optional local hybrid search

`/memory hybrid on` explicitly downloads and verifies Qwen3-Embedding 0.6B and Qwen3-Reranker 0.6B. It requires Ollama and Python 3.10+ with venv support. `/memory hybrid off` immediately restores keyword-only retrieval without deleting models or vectors; open chats apply changes on the next message.

Startup uses cached models only. The [vector cache](../internal/memory/semantic.go) fills lazily during semantic searches, with bounded backfill of missing or changed vectors; it is not a continuously running indexer. Embedding or reranking failures fall back to keywords with a stage-specific notice. `kh memory explain "query"` exposes fallback status; `/memory hybrid on` retries setup and verification. Optional `memory_embedding_url`, `memory_embedding_model`, and `memory_reranker_url` settings override defaults. Managed runtime files live in `~/.kh/hybrid`.

The [managed runtime](../internal/hybrid/runtime.go) authenticates reranker helper revisions. Startup does not replace another build’s helper, and mismatched scoring requests fail rather than silently using a different prompt. Explicit hybrid setup upgrades owned helpers gracefully; unknown legacy helpers require a manual stop instead of automatic termination. Reranking has a 25-second request budget, including up to 16 seconds waiting for workers and 8 seconds for inference.

## Procedures are inert references

[Procedural references](../internal/memory/references.go) store full workflows, aliases, and inert script assets in the same database. Names, aliases, and topics retrieve compact hints; the tool’s `load` action fetches the full reference. There is no filesystem-backed runtime skills library. These manual pages explain design; they are not a second runtime knowledge store.

The [procedure learner](../internal/learn/procedures.go) recognizes useful, non-obvious workflows backed by multiple successful tool outputs and can save them without a review prompt. Equivalent procedures are reused; material improvements require fresh evidence and version the existing reference. Extraction can decline or fail: tool use alone does not establish a reusable procedure.

Use `kh memory reference put/get/export/import/asset` to manage references. `kh memory reference demo` stores a sound example without playing it. Imports preserve provenance without executing assets or overwriting existing topics. Saving, retrieving, loading, and importing never execute scripts; requested execution goes through ordinary approved, sandboxed tools.

See the [memory architecture diagram](../memory-architecture.svg) for retrieval, learning, and reference-loading flows.
