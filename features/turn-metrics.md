# Turn metrics

Expose response latency and context costs alongside each turn.

Introduced **2026-09-28** · commit `0316225`.

## Current behavior

Per-turn statistics show time to first token (TTFT), elapsed time, input/cache/output/reasoning tokens, and context use. These are turn-level feedback, not an aggregate dashboard or a billing ledger. The display uses metrics supplied by the provider and does not imply support for additional backends.

The [roughly 5,000-token fresh-session target](project-configuration.md#compact-context-and-tool-discovery) is not a universal history cap or a guaranteed measured total. Binding guidance, user input, loaded references, tool results, and accumulated conversation history can increase actual context; use provider-reported turn metrics to assess a real request.

An identical-prompt provider comparison reported **2,239 input tokens without hosted web search** versus **6,703 with it**, with zero tool calls in either run. This isolates hosted-schema overhead for that comparison, not the full CLI greeting or a universal saving. A subsequent fresh full CLI greeting using the unchanged real-HOME configuration exited successfully and displayed **2.3k input tokens** (rounded to 0.1k), with zero tool or hosted-search actions. That CLI reading is distinct from the exact controlled comparison.

## Assignment phase timings

Native `inspect`/`peek` adds `task_status` with the current lease, assignment status, reusable-worker flag, and `timings_ms`: `startup_ms`, `context_ms`, `execution_ms`, and `report_ms`. These host-side task measurements are separate from provider TTFT, token accounting, and billing. `startup_ms` measures setup inside the task-worker entry point for its initial task, not the entire parent-to-child launch or launcher rebuild. `context_ms` measures foundation resolution and, for model tasks, successful provider/tool/MCP setup before the turn; `execution_ms` measures the model turn itself or the Git workflow including its approval wait. `report_ms` measures final output preparation/flushing, not the subsequent registry write or timing notice. Reservation resets phase values for a new generation; task completion stores its measurements under the process- and generation-fenced lease. A zero value can mean a phase was not measured, not proof that it cost nothing.

The goal for [verified-state Git push](shell.md#verified-git-push) is roughly ten seconds to push and confirm an already-committed, previously checked change under normal conditions. This is a design target, **not a latency guarantee or a measured benchmark result**. Git hooks, credentials, network conditions, startup and source validation can change elapsed time. The [foundation cache](project-configuration.md#assignment-foundations) avoids reparsing on validated hits, but validation still performs filesystem I/O; caching alone is not evidence of a speedup.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added TTFT, elapsed time, and input/cached/output token statistics. | `0316225` |
| 2026-09-28 | Added thinking-token reporting. | `6c5ea3e` |
| 2026-09-28 | Corrected TTFT timing. | `b1d3055` |
| 2026-09-28 | Added context used/limit reporting. | `8ad92f0` |
| 2026-10-16 | Gave statistics distinct visual styling. | `efde4fb` |
| 2026-11-03 | Distinguished the fresh-session baseline target and controlled hosted-schema comparison from actual CLI turn usage. | Current change |
| 2026-11-05 | Added assignment phase timing metadata separate from provider turn statistics. | Current change |

## Inspect

- [Turn display](../cmd/kh/chat.go) and [provider metrics contract](../internal/provider/provider.go)
- [Codex implementation tests](../internal/provider/codex/test/codex_test.go)
- [Assignment timing boundaries](../cmd/kh/worker.go), [status and timing storage](../internal/ecosystem/tasks.go), [lease/timing coverage](../internal/ecosystem/test/tasks_test.go)
- [Presentation and context](architecture.md#presentation-and-context)

[Feature index](README.md)
