# Steering and stopping

Redirect or stop active work without restarting the chat.

Introduced 2026-09-28 · commit `3354dc0` (per-turn stopping; typed steering came later).

## Current behavior

Type during a reply to steer. Ctrl-C stops/discards the active reply; Ctrl-D on empty input exits. Running tools finish before steering is applied. Completed effects are not rolled back, so stopping a turn does not undo filesystem or external changes.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added chat with Ctrl-C stopping only the current turn. | `3354dc0` |
| 2026-09-28 | Added typed steering between model/tool steps. | `054e0b3` |
| 2026-09-28 | Made steering cancel an in-progress reply immediately while still waiting for running tools. | `8ad92f0` |
| 2026-10-16 | Applied the pre-message hook to steering and queued messages as well as initial input. | `46c225b` |

## Inspect

- [Runner](../internal/agent/loop.go) and [steering tests](../internal/agent/test/loop_test.go).
- [Turn-loop architecture](../manual/architecture.md#application-and-turn-loop) and [essential controls](../README.md#use).

[Feature index](README.md)
