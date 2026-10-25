# Evidence-backed workflow learning

Retain reusable methods from successful work rather than merely accumulating facts.

Introduced **2026-10-23** · commit `23e7a86`.

## Current behavior

When automatic learning is enabled in a user-facing chat, the background learner can save named procedures without a review prompt. Candidates must represent useful, non-obvious workflows supported by multiple successful tool outputs. Equivalent procedures are reused; material improvements require fresh evidence and version the existing reference.

Extraction may decline or fail: tool use alone does not establish a reusable workflow. See [learning hooks](learning-hooks.md) for completion timing, [queue behavior](learning-queue.md) for deadlines and drops, and [origin filtering](learning-origin.md) for worker/peer exclusions. Saved methods become [on-demand references](procedure-loading.md), not permission to execute them.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Introduced autonomous procedure extraction, evidence checks, equivalent-reference reuse, and versioned improvements on the existing reference store. | `23e7a86` |

## Inspect

- Source: [procedure learner](../internal/learn/procedures.go), [background scheduling](../internal/learn/learn.go), [tool-less extraction](../internal/learn/ask.go).
- Tests: [procedures](../internal/learn/test/procedures_test.go), [extraction calls](../internal/learn/test/ask_test.go).

[Feature index](README.md)
