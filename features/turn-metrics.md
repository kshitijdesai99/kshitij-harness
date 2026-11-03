# Turn metrics

Expose response latency and context costs alongside each turn.

Introduced **2026-09-28** · commit `0316225`.

## Current behavior

Per-turn statistics show time to first token (TTFT), elapsed time, input/cache/output/reasoning tokens, and context use. These are turn-level feedback, not an aggregate dashboard or a billing ledger. The display uses metrics supplied by the provider and does not imply support for additional backends.

The [roughly 5,000-token fresh-session target](project-configuration.md#compact-context-and-tool-discovery) is not a universal history cap or a guaranteed measured total. Binding guidance, user input, loaded references, tool results, and accumulated conversation history can increase actual context; use provider-reported turn metrics to assess a real request.

An identical-prompt provider comparison reported **2,239 input tokens without hosted web search** versus **6,703 with it**, with zero tool calls in either run. This isolates hosted-schema overhead for that comparison, not the full CLI greeting or a universal saving. A subsequent fresh full CLI greeting using the unchanged real-HOME configuration exited successfully and displayed **2.3k input tokens** (rounded to 0.1k), with zero tool or hosted-search actions. That CLI reading is distinct from the exact controlled comparison.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added TTFT, elapsed time, and input/cached/output token statistics. | `0316225` |
| 2026-09-28 | Added thinking-token reporting. | `6c5ea3e` |
| 2026-09-28 | Corrected TTFT timing. | `b1d3055` |
| 2026-09-28 | Added context used/limit reporting. | `8ad92f0` |
| 2026-10-16 | Gave statistics distinct visual styling. | `efde4fb` |
| 2026-11-03 | Distinguished the fresh-session baseline target and controlled hosted-schema comparison from actual CLI turn usage. | Current change |

## Inspect

- [Turn display](../cmd/kh/chat.go) and [provider metrics contract](../internal/provider/provider.go)
- [Codex implementation tests](../internal/provider/codex/test/codex_test.go)
- [Presentation and context](architecture.md#presentation-and-context)

[Feature index](README.md)
