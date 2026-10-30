# Command approval policy

Control command execution without restricting which folders kh can access.

Introduced **2026-09-28** · commit `84d64c8`.

## Write boundaries

kh does not impose a filesystem sandbox or folder-grant boundary. Shell commands and structured edits can access locations outside the launch folder, subject to OS privacy and file permissions. [Edit path protections](edit-path-safety.md) prevent accidental path redirection; they do not confine edits to a project.

## Current behavior

Automatic command approval is **on by default**. `--auto=false` enables prompts for one session; `--auto` enables automatic approval for one session. `/auto off` and `/auto on` persist the preference. Flags override configuration.

- Turning auto off restores command approval prompts; configured safe commands can still run without a prompt. It does not change filesystem access or add edit prompts.
- [Workers](worker-lifecycle.md) and [new/forked chats](chat-branches.md) inherit the active approval policy.
- Denied command approval stops the command before execution. OS permission failures and edit-path validation can still stop operations; commands and edit batches do not roll back earlier effects.
- MCP startup trust and per-call approval also use auto policy; see [project configuration](project-configuration.md#mcp-and-safety).
- Approval is not a correctness guarantee or a filesystem security boundary.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial shell command approval prompts. | `84d64c8` |
| 2026-09-29 | Added the `--auto` workflow. | `595802d` |
| 2026-10-16 | Added process-lifetime outside-folder grants. | `09b6775` |
| 2026-10-17 | Added automatic write grants, then unified command/folder policy under auto. | `77639f2`, `79f1ce2` |
| 2026-10-23 | Made automatic approval the default. | `bf2c75d` |
| 2026-10-31 | Removed folder grants and filesystem confinement; retained command auto policy. | Current change |

## Inspect

- [Command policy and shell execution](../internal/tools/bash.go)
- [Auto-policy and cross-directory command tests](../internal/tools/test/auto_test.go)

[Feature index](README.md)
