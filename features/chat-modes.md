# Interactive or headless tasks

Choose between an ongoing chat and a task that exits after completion.

Introduced 2026-09-28 · commit `3354dc0` (interactive chat alongside existing one-shot execution).

## Current behavior

Chat is the default: `kh` opens a conversation, and `kh "TASK"` runs a task and stays in chat. Use `kh -p "TASK"` for headless execution that exits after the task. `-i` remains a compatibility alias for interactive behavior; `-p` requires a task and cannot be combined with `-i`.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial harness ran one-shot tasks. | `84d64c8` |
| 2026-09-28 | Added interactive chat when launched without a task. | `3354dc0` |
| 2026-09-28 | Added `-i` to stay in chat after an initial task. | `5eb25e9` |
| 2026-10-23 | Made interactive execution the default and added explicit `-p` headless mode. | `bf2c75d` |

## Inspect

- [CLI](../cmd/kh/main.go) and [mode tests](../cmd/kh/test/mode_test.go).
- [Essential usage](../README.md#use) and [session manual](../manual/safety-and-sessions.md).

[Feature index](README.md)
