# Background learning queue and summaries

Bound automatic retention work so learning does not block the chat answer.

Introduced **2026-10-05** · commit `0a203a6`.

## Current behavior

When automatic learning is enabled, one worker processes a bounded 32-job queue serially. Each job has a cancellable two-minute context; a full queue drops work with a notice rather than blocking chat. Extraction uses fresh tool-less model calls and can decline or fail. Notices are displayed between turns, not a complete audit of every attempt.

Summaries rebuild from current notes only when those notes change. Startup queues a catch-up summary check; shutdown allows up to 30 seconds for queued work before cancellation. Answering does not wait for a save, and separate processes still need store revision checks to avoid stale updates.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Introduced serial background hooks, bounded work, notices, and summaries rebuilt from current notes. | `0a203a6` |
| 2026-10-16 | Expanded selective extraction with discussion context and event timestamps. | `46c225b` |
| 2026-10-17 | Added guarded memory-update decisions to prevent weak or unsupported replacements. | `77639f2` |
| 2026-10-23 | Added eligible procedure extraction to completion jobs. | `23e7a86` |

## Inspect

- Source: [queue and summaries](../internal/learn/learn.go), [update guards](../internal/learn/update.go), [startup/shutdown](../cmd/kh/main.go).
- Tests: [learning and summaries](../internal/learn/test/learn_test.go), [selectivity and queue behavior](../internal/learn/test/selective_test.go), [updates](../internal/learn/test/update_test.go).
- Manual: [background-job pipeline](../manual/memory.md#hook-and-background-job-pipeline).

[Feature index](README.md)
