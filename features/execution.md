# Model/tool loop

Work through a request by alternating model replies and executable tool batches.

Introduced 2026-09-28 · commit `84d64c8`.

## Current behavior

Automatic: the runner asks the provider for the next step until no tool calls remain. Tool batches run concurrently and results return in call order. Tools share files, so dependent work requires coordination rather than relying on call ordering.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Introduced the model/tool loop with concurrent, ordered results. | `84d64c8` |
| 2026-10-01 | Separated backend protocol/state from runner orchestration. | `60e2bd1` |
| 2026-10-16 | Added a pre-message hook for actual initial, steering, and queued messages, preserving completed tool results on hook failure. | `46c225b` |

## Inspect

- [Runner](../internal/agent/loop.go) and [hook/order tests](../internal/agent/test/message_hook_test.go).
- [Architecture and turn loop](architecture.md#application-and-turn-loop).

[Feature index](README.md)
