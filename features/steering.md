# Steering and stopping

Redirect or stop active work without restarting the chat.

Introduced 2026-09-28 · commit `3354dc0` (per-turn stopping; typed steering came later).

## Current behavior

Submitting a new message with Enter during a reply steers the model: kh cancels the partial reply, waits for provider cleanup, and submits the new message. Unsubmitted typing stays in the editor, including when [peer messages arrive](input.md#peer-messages-and-drafts). Already-running tools finish before steering takes effect so results remain paired with calls.

The [turn execution and steering diagram](execution.md#turn-execution-and-steering) shows both paths: reply steering waits for cancellation cleanup, while tool-time steering retains ordered results for pairing in the next provider step. Initial, steering, and queued nonempty messages all pass through image processing and the pre-message hook; empty-message result steps do not run that hook.

**Ctrl-C** stops a task or discards the draft; **Ctrl-D** on empty input quits. Steering and stopping are **not a rollback**: completed filesystem or external changes are not undone. See [input controls](input.md) for composing a message.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added chat with Ctrl-C stopping only the current turn. | `3354dc0` |
| 2026-09-28 | Added typed steering between model/tool steps. | `054e0b3` |
| 2026-09-28 | Made steering cancel an in-progress reply immediately while still waiting for running tools. | `8ad92f0` |
| 2026-10-16 | Applied the pre-message hook to steering and queued messages as well as initial input. | `46c225b` |

## Inspect

- [Runner](../internal/agent/loop.go) and [steering tests](../internal/agent/test/loop_test.go).
- [Turn execution](execution.md) and [provider-owned history](provider-state.md).

[Feature index](README.md)
