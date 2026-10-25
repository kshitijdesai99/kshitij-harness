# Shell commands

Build, inspect, and run tools through shell commands in the working directory.

Introduced 2026-09-28 · commit `84d64c8`.

## Current behavior

Use `bash`; command timeout and returned output cap are configurable. Approval and write policy apply: execution access is not a guarantee that a command is safe. The OS-level write sandbox is macOS-only; other Unix systems do not provide the equivalent shell restriction.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Introduced shell execution with bounded time and output. | `84d64c8` |
| 2026-09-28 | Moved timeout and output limits into configuration. | `4f09a5a` |
| 2026-09-28 | Added macOS shell-write sandboxing. | `83bb8cd` |
| 2026-10-16 | Added declared outside-folder writes and process-lifetime folder approvals. | `09b6775` |

## Inspect

- [Shell implementation](../internal/tools/bash.go), [configuration](../internal/config/config.go), and [tool tests](../internal/tools/test/tools_test.go).
- [Approval and write boundaries](../manual/safety-and-sessions.md#approval-and-write-boundaries).

[Feature index](README.md)
