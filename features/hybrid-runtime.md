# Managed hybrid helper

Bound local retrieval-model contention while keeping optional hybrid search recoverable.

Introduced **2026-10-23** · commit `6fc58c6`.

## Current behavior

This helper supports [optional hybrid retrieval](hybrid-retrieval.md), which owns setup requirements, mode controls, and keyword fallback behavior.

Managed runtime files live in `~/.kh/hybrid`. Reranking has a 25-second request budget, including up to 16 seconds waiting for workers and 8 seconds for inference.

The runtime authenticates helper revisions. Startup does not replace another build's helper; mismatched scoring requests fail rather than silently using a different prompt. Explicit hybrid setup can gracefully upgrade an authenticated owned helper. Unknown listeners and legacy helpers are not stopped automatically; an unknown legacy helper requires a manual stop. [Retrieval diagnostics](retrieval-diagnostics.md) expose helper/fallback status.

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
- [Hybrid setup and fallback](hybrid-retrieval.md)

[Feature index](README.md)
