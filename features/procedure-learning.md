# Evidence-backed workflow learning

Retain reusable methods from successful work rather than merely accumulating facts.

Introduced **2026-10-23** · commit `23e7a86`.

## Current behavior

When automatic learning is enabled in a user-facing chat, the background learner can save named procedures without a review prompt. Candidates must represent useful, non-obvious workflows supported by multiple successful tool outputs. Equivalent procedures are reused; material improvements require fresh evidence and version the existing reference.

Extraction may decline or fail: tool use alone does not establish a reusable workflow. Worker/peer-origin exclusions still apply. Procedures and any script assets remain inert references; learning a method does not authorize or execute it.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Introduced autonomous procedure extraction, evidence checks, equivalent-reference reuse, and versioned improvements on the existing reference store. | `23e7a86` |

## Inspect

- Source: [procedure learner](../internal/learn/procedures.go), [background scheduling](../internal/learn/learn.go), [tool-less extraction](../internal/learn/ask.go).
- Tests: [procedures](../internal/learn/test/procedures_test.go), [extraction calls](../internal/learn/test/ask_test.go).
- Manual: [procedures are inert references](../manual/memory.md#procedures-are-inert-references).

[Feature index](README.md)
