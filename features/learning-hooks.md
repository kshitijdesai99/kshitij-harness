# Query and response learning hooks

Selectively retain lasting lessons without making extraction part of the foreground answer.

Introduced **2026-10-05** · commit `0a203a6`.

## Current behavior

Automatic learning is enabled in user-facing chats unless `memory_model` is `off`; retrieval remains available when saving is off. After pasted-image handling, each actual nonempty initial, steering, or queued user message reaches `Runner.BeforeMessage` before the provider step. Empty tool-result steps do not trigger it.

The pre-hook queues extraction with recent discussion and event time. Successful, uncanceled responses supply bounded reply/tool evidence to the completion hook. Neither hook is an exhaustive archive or guaranteed save; origin filtering excludes delegated and peer-derived instructions.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Added pre-query and post-response background hooks. | `0a203a6` |
| 2026-10-05 | Removed model-tool saving so automatic writes go through reported hooks. | `8ab0023` |
| 2026-10-16 | Moved query hooks into the runner for steering/queued messages; supplied context and timestamps, and excluded canceled completion hooks. | `46c225b` |
| 2026-10-23 | Fenced peer-coordinated completion evidence and added procedure extraction. | `23e7a86` |

## Inspect

- Source: [chat wiring](../cmd/kh/chat.go), [runner](../internal/agent/loop.go), [learner](../internal/learn/learn.go).
- Tests: [message hooks](../internal/agent/test/message_hook_test.go), [selective learning](../internal/learn/test/selective_test.go).
- Manual: [hook and background-job pipeline](../manual/memory.md#hook-and-background-job-pipeline).

[Feature index](README.md)
