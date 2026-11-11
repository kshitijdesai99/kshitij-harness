# Bounded keyword recall

Supply relevant stored knowledge before answering without requiring local model downloads.

Introduced **2026-09-29** · commit `14dd3e6`.

## Current behavior

Keyword-only retrieval is the default and requires no model downloads. After each actual user query arrives, including steering and queued messages, and before the provider answers, recall considers repo and global memory within `memory_max_tokens` (estimated as characters/4). A bounded critical section precedes contextual matching; relevant [full database procedures](procedure-loading.md) can enter automatically when their complete content fits. [History](memory-history.md) remains available on demand. See [learning hooks](learning-hooks.md) for timing relative to saving.

The default `memory_max_tokens` is **500**, reduced from 4,000 for [compact startup context](project-configuration.md#compact-context-and-tool-discovery). Explicit values in `~/.kh/config.json` are retained, including an existing 4,000 override or `0`; this change does not rewrite saved configuration. `summary_max_tokens` remains 2,500, but automatic injection still respects the total memory budget. Nonpositive total budgets retain the legacy 16,000-character fallback; `0` does not disable recall. These are character-based estimates, not provider-token measurements.

### Core, critical, contextual, and history

- **Core:** short runtime behavior and safety guidance stays in the harness policy, not duplicated into every memory section.
- **Critical:** active, current repo/global notes classified critical are considered before the relevance gate, including on greetings. Exact notes use a bounded share of the existing total. Selection is deterministic: repo-local before global, then key and ID; oversized whole notes are skipped without blocking later smaller notes. Packing happens before seen filtering, and enabled critical notes do not rotate into the contextual path to bypass the cap. Overflow is reported in sanitized recall diagnostics rather than silently claiming everything loaded. The inline omission notice is best-effort: a tiny total or higher-priority corrections can leave no room for its text. A fresh, whole contextual synopsis is derived separately from canonical notes, never used as a replacement for exact selected critical notes.
- **Contextual:** ordinary notes and relevant procedures use keyword, optional semantic, and exact-key/name retrieval. Importance changes admission order, never instruction authority: memory remains below core instructions and the current user request.
- **History:** superseded, forgotten, and expired versions remain inspectable, not routinely injected.

Corrections and expiry voids have priority over new critical admission: they may exceed the dedicated critical share to retire stale context, but never exceed the overall total.

`critical_memory_max_tokens` defaults to **0**, meaning half the normalized total automatically; a positive value is clamped to that total, and a negative value disables only the critical section. No extra total allowance is added. Headings, overflow notices, synopsis, and procedure bodies all consume the total. Repeated turns use seen tracking rather than appending the same critical block forever. This caps newly admitted memory, not the entire saved provider history, and cannot guarantee model obedience or complete recall.

Common standalone greetings, check-ins, acknowledgments, and knowledge already present in chat avoid unnecessary topic retrieval. Critical memory, corrections, and explicitly named references remain candidates, subject to freshness, seen tracking, and budget limits. Selection favors applicable notes with [provenance and replacement relationships](memory-history.md), not the volume of saved knowledge. Retrieval can miss or fail; relevance and completeness are not guaranteed. [Optional hybrid reranking](hybrid-retrieval.md) can reject weak matches rather than filling a quota. Use [diagnostics](retrieval-diagnostics.md) to inspect selection; current user requests override retrieved reference data. An intact current note can still miss lexical retrieval when the query uses different words or word forms; hybrid configuration alone does not prove that semantic retrieval or reranking succeeded on a particular turn.

## Retrieval flow

![Recall before answering: required recall survives topic-search skips; bounded new context and diagnostics follow.](../diagrams/retrieval-flow.svg)

Critical memory, corrections, and named references are considered before the topic-search gate, not bypassed by it. They still respect seen tracking and the context budget. Only successfully injected notes become seen; budget-skipped notes remain eligible later. Seen tracking trusts [host memory provenance](learning-origin.md#host-memory-provenance), not a user- or tool-supplied memory-looking marker.

The diagram follows [chat wiring](../cmd/kh/chat.go), [recall ordering](../internal/memory/recall.go), and [topic selection](../internal/memory/search.go). Search-stage failure and a successful search with no relevant topic notes are different outcomes.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Introduced keyword discovery with a small matching-detail and character cap before each answer. | `14dd3e6` |
| 2026-10-05 | Replaced discovery records with append-only notes, summary-aware recall, and conversation-based seen tracking. | `0a203a6` |
| 2026-10-16 | Added broader knowledge selection, a total context budget, and recall for actual steering/queued messages. | `46c225b` |
| 2026-10-23 | Added compact procedure hints and explicit reference matching. | `f802b27` |
| 2026-10-23 | Improved applicability selection, then skipped unnecessary topic retrieval. | `24e64b7`, `13e26fd` |
| 2026-11-02 | Reduced the default automatic memory budget to 500 estimated tokens without changing explicit configuration or recall/learning timing. | Current change |

## Inspect

- Source: [recall](../internal/memory/recall.go), [keyword selection](../internal/memory/search.go), [configuration defaults and overlay](../internal/config/config.go).
- Default/override coverage: [configuration tests](../internal/config/test/config_test.go).
- Tests: [selection](../internal/memory/test/selection_test.go), [recall efficiency](../internal/memory/test/recall_efficiency_test.go), [reference recall](../internal/memory/test/reference_recall_test.go), [hierarchy and freshness](../internal/memory/test/hierarchy_test.go).

[Feature index](README.md)


## 2026-11-11 — Bounded critical-first admission without higher memory authority

- Status: implemented
- Rationale: Two sanitized Fable5.2 xhigh consultation rounds retained versioned tier/expiry, deterministic whole-note critical packing before relevance, a source-fresh whole contextual synopsis, and relevant inert procedure-body loading. They rejected a quote ledger, extra evidence/capacity fields, per-line citations, a new daemon, and repeated critical reinjection. Critical rank never overrides core instructions or the current request. The existing total budget remains fixed; visible overflow and explicit inspection acknowledge finite coverage, and seen tracking cannot bound the entire saved provider history.
- Affected: internal/memory/recall.go; internal/config/config.go; normal and task-fresh worker wiring; keyword-recall.md; retrieval-flow.svg; linked hierarchy/configuration tests
- Verification / outstanding: Final `KH_AUTO_REBUILD=0 go test ./...` and `go vet ./...` passed. Race checks passed for memory and the unchanged learning/provider/configuration/tool production scope; the affected CLI hybrid race case passed after correcting its stale diagnostic assertion. The entire post-fix CLI race aggregate was not rerun, so no blanket full-CLI race pass is claimed. Independent reviews found no remaining demonstrated defects after fixes. Diagram owner rendered and visually inspected full/detail images, checked XML/incoming links, and verified installed SVG equality. Changed documentation links/anchors, formatting and `git diff --check` passed. Remaining limitations are documented above; model compliance and complete recall are not guaranteed.
