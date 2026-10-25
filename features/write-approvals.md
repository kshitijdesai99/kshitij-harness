# Approval policy and folder grants

Control command execution and outside-project writes with explicit, session-scoped policy.

Introduced **2026-09-28** · commit `84d64c8` (command approvals; folder grants followed).

## Current behavior

Automatic command and requested-folder approval is **on by default**. `--auto=false` enables prompts for one session; `--auto` enables automatic approval for one session. `/auto off` and `/auto on` persist the preference. Flags override configuration.

Shell calls must declare desired existing outside write folders with `write_dirs`; [edits](edit-path-safety.md) request outside-folder access automatically. Configured shell-writable directories are not implicit edit grants. Auto approval retains [sandbox](shell-sandbox.md) and pinned-path checks; it does not grant the entire filesystem at startup.

Turning auto off restores command and new-folder prompts, but existing grants last until the process [restarts](startup-rebuild.md#restart-onto-a-new-build). [Workers](worker-lifecycle.md) and [new/forked chats](chat-branches.md) inherit active policy, **not directory grants**. Approval is not a correctness guarantee, and without an OS sandbox prompts alone are not a security boundary.

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

[Feature index](README.md)
