# Approval policy and folder grants

Control command execution and outside-project writes with explicit, session-scoped policy.

Introduced **2026-09-28** · commit `84d64c8` (command approvals; folder grants followed).

## Current behavior

Automatic command and requested-folder approval is on by default. Use `--auto=false` for session prompts or `/auto off` to persist that preference; flags override configuration. Bash declares outside write folders with `write_dirs`; edits request access automatically. Grants last for the running process and remain after auto is turned off, until restart. Workers and new/forked chats inherit policy, not directory grants.

Auto retains sandbox and pinned-path checks; it does not pre-authorize the whole filesystem. Approval is not a correctness guarantee, and without an OS sandbox prompts alone are not a security boundary.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial shell command approval prompts. | `84d64c8` |
| 2026-09-29 | Added the `--auto` workflow. | `595802d` |
| 2026-10-16 | Added process-lifetime outside-folder grants. | `09b6775` |
| 2026-10-17 | Added automatic write grants, then unified command/folder policy under auto. | `77639f2`, `79f1ce2` |
| 2026-10-23 | Made automatic approval the default. | `bf2c75d` |

## Inspect

- [Access control](../internal/tools/access.go) and [shell](../internal/tools/bash.go)
- [Auto-policy tests](../internal/tools/test/auto_test.go) and [access tests](../internal/tools/test/access_test.go)
- [Operating policy](../manual/safety-and-sessions.md#approval-and-write-boundaries)

[Feature index](README.md)
