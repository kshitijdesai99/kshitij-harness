# Turn metrics

Expose response latency and context costs alongside each turn.

Introduced **2026-09-28** · commit `0316225`.

## Current behavior

Per-turn statistics show time to first token (TTFT), elapsed time, input/cache/output/reasoning tokens, and context use. These are turn-level feedback, not an aggregate dashboard or a billing ledger. The display uses metrics supplied by the provider and does not imply support for additional backends.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added TTFT, elapsed time, and input/cached/output token statistics. | `0316225` |
| 2026-09-28 | Added thinking-token reporting. | `6c5ea3e` |
| 2026-09-28 | Corrected TTFT timing. | `b1d3055` |
| 2026-09-28 | Added context used/limit reporting. | `8ad92f0` |
| 2026-10-16 | Gave statistics distinct visual styling. | `efde4fb` |

## Inspect

- [Turn display](../cmd/kh/chat.go) and [provider metrics contract](../internal/provider/provider.go)
- [Codex implementation tests](../internal/provider/codex/test/codex_test.go)
- [Presentation and context](../manual/architecture.md#presentation-and-context)

[Feature index](README.md)
