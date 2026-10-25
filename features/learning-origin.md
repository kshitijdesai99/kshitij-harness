# Learning origin filtering

Keep delegated tasks and peer coordination from becoming durable user instructions.

Introduced **2026-10-16** · commit `46c225b`.

## Current behavior

Workers retrieve shared memory but do not create a learner for their delegated task prompts. User-facing learning rejects peer-message inputs, including mixed user/peer composites.

Once a run includes peer coordination, its tool evidence, final reply, and recent learning context are excluded from post-response learning, even if a user steers later. Separate clean user queries still receive the pre-hook. This conservative boundary deliberately forgoes some learning to avoid attributing worker directives to the user.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-16 | Disabled worker learning at startup and rejected messages beginning with a peer marker. | `46c225b` |
| 2026-10-23 | Detected peer markers throughout composite input and added a sticky run-origin fence for completion evidence and recent context. | `23e7a86` |

## Inspect

- Source: [startup boundary](../cmd/kh/main.go), [peer-input checks](../internal/learn/learn.go), [run origin](../internal/learn/origin.go), [chat wiring](../cmd/kh/chat.go).
- Tests: [origin fencing](../internal/learn/test/origin_test.go), [selective learning](../internal/learn/test/selective_test.go).
- Manual: [hook and background-job pipeline](../manual/memory.md#hook-and-background-job-pipeline).

[Feature index](README.md)
