# Retrieval explanations

Inspect why memory was selected, rejected, skipped, or replaced by fallback.

Introduced **2026-10-23** · commit `24e64b7`.

## Current behavior

Use `kh memory status` to inspect configuration and `kh memory explain "QUERY"` to run a content-free search explanation, including [hybrid fallback](hybrid-retrieval.md). The native memory tool's `explain` action accepts the same filters as `search` but returns only scope labels and stage counts. Normal `search` still returns matching notes; use [memory controls](memory-controls.md) to inspect stored content and provenance.

Search explanations report keyword-term/candidate counts and semantic/rerank configured, attempted, complete, fallback, scored/rejected and selected counts. CLI explain also distinguishes requested hybrid mode from a successfully configured pipeline; configured does not mean the services are healthy. Attempted means the pipeline entered that stage, not necessarily that a model received a request. Configuration/startup failures may leave keyword-only retrieval even when hybrid was requested.

In chat, `/memory explain` reuses the last recall's candidate/loaded IDs, scores, rejection/skip details and timing without searching again. Per-message summaries bound the displayed IDs. Diagnostic output omits queries, note keys/bodies, provider errors, private paths and model identities; loaded memories use IDs, not keys. This does not redact ordinary search/get output or other CLI errors.

A successful stage does not prove relevance or model compliance, and an empty load can reflect no new applicable knowledge or the context budget. These controls are not a unified event trace or a record of every learning attempt.

## Trace in context

The [retrieval flow](keyword-recall.md#retrieval-flow) separates required recall, skipped topic search, model-stage fallback, and zero relevant topic results. The trace records the actual recall, including loaded IDs outside topic search; selected candidates are not necessarily injected when seen tracking or the total budget excludes them. In-chat explain reuses that trace rather than running retrieval again.

Check current scope/version and stored content separately from retrieval: a lexical miss does not establish storage corruption, and related word forms need not match. A new explanation reproduces current behavior, not a historical turn. Hybrid being enabled alone cannot establish the cause of an earlier miss without that turn's trace.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Added retrieval trace formatting and explain controls alongside relevance improvements. | `24e64b7` |
| 2026-10-23 | Added explicit skip reasons, bounded per-message summaries, and clearer helper/fallback reporting. | `13e26fd` |

## Inspect

- Source: [diagnostic formatting](../internal/memory/diagnostic.go), [recall](../internal/memory/recall.go), [CLI](../cmd/kh/memory.go), [chat](../cmd/kh/chat.go).
- Tests: [diagnostics](../internal/memory/test/diagnostic_test.go), [recall efficiency](../internal/memory/test/recall_efficiency_test.go), [CLI privacy and fallback](../cmd/kh/test/memory_test.go), [chat explain reuse](../cmd/kh/test/hybrid_test.go).

[Feature index](README.md)


## 2026-11-10 — Sanitized retrieval explanations before engine changes

- Status: implemented
- Rationale: Separate storage/version checks from retrieval evidence. Add count-only native/CLI search explanations and ID-only recall loading diagnostics, without queries, keys, note bodies, raw provider errors, private paths or model identities. Preserve ordinary search results and the existing engine/schema/extractor. Hybrid configuration alone cannot explain a historical miss without that turn's trace.
- Affected: internal/memory search/recall/diagnostics, internal/tools memory explain, cmd/kh/memory.go; current behavior and evidence above.
- Verification / outstanding: Focused CLI memory/privacy/hybrid tests passed (go test ./cmd/kh/test -run 'TestMemory(CLI|HybridCLI)' -count=1), including keyword/hybrid success, provider failures and invalid endpoint configuration. git diff --check passed. Historical incident causality and model compliance remain unproven; these controls do not reconstruct past traces.
