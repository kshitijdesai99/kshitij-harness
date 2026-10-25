# Managed hybrid helper

Bound local retrieval-model contention while keeping optional hybrid search recoverable.

Introduced **2026-10-23** · commit `6fc58c6`.

## Current behavior

This helper supports [optional hybrid retrieval](hybrid-retrieval.md), which owns setup requirements, mode controls, and keyword fallback behavior.

The managed helper has queue/inference deadlines and authenticated revision checks. Startup does not replace a different helper revision; explicit setup can gracefully upgrade an authenticated owned helper. Unknown listeners are not stopped automatically. Retrieval diagnostics are available through `kh memory explain`; detailed setup belongs in the memory manual.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Added opt-in hybrid memory and the managed local runtime. | `6fc58c6` |
| 2026-10-23 | Bounded reranker contention and request timeouts. | `d49942b` |
| 2026-10-23 | Fixed hybrid worker bursts. | `5581835` |
| 2026-10-23 | Fenced helper revisions and authenticated upgrades. | `13e26fd` |

## Inspect

- [Runtime](../internal/hybrid/runtime.go) and [helper](../internal/hybrid/reranker.py)
- [Runtime tests](../internal/hybrid/test/runtime_test.go) and [helper tests](../internal/hybrid/test/helper_test.py)
- [Hybrid setup, limits, and fallback](../manual/memory.md#optional-local-hybrid-search)

[Feature index](README.md)
