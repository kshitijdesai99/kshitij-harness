# Shell commands

Build, inspect, and run tools through shell commands in the working directory.

Introduced 2026-09-28 · commit `84d64c8`.

## Current behavior

Use `bash`; command timeout and returned output cap are configurable. [Command approval](write-approvals.md) applies, but filesystem access is unrestricted by kh, including outside the launch folder, subject to OS permissions. Execution access is not a guarantee that a command is safe.

## Verified Git push

Native delegation supports an explicit `push_verified` [handoff workflow](native-agents.md#task-handoffs) for work that is already committed and checked. It is not a standalone shell tool, a generic Git assistant, or an automatic shortcut inferred from words such as “push.” Supply the expected full lowercase commit ID (40 or 64 hex characters), branch, exact configured upstream such as `origin/main`, and nonblank prior verification references. These references are caller-supplied evidence, not independently revalidated test results.

The workflow makes no model calls, commits, or test reruns. It checks that HEAD equals the expected commit, the attached branch and configured remote upstream match, and the tracked and untracked working tree is clean, including submodule status. It requires one configured push URL, rechecks local state, then performs a normal non-force push of the pinned commit to that one upstream branch. Tag following, mirror pushes, and recursive submodule pushes are disabled; ordinary Git hooks still run. Ambient `GIT_*` overrides are removed, but normal on-disk configuration and hooks are not sandboxed.

Success requires a subsequent `ls-remote` check confirming that the remote branch equals the expected commit. The operation has a 30-second total timeout and a 64 KiB output limit per Git command. A push or remote-confirmation failure can happen **after the remote changed**; report that uncertainty rather than falling back to a less constrained push. Concurrent external repository changes cannot be locked out, and the supplied evidence is not a substitute for choosing the correct verified commit.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Introduced shell execution with bounded time and output. | `84d64c8` |
| 2026-09-28 | Moved timeout and output limits into configuration. | `4f09a5a` |
| 2026-09-28 | Added macOS shell-write sandboxing. | `83bb8cd` |
| 2026-10-16 | Added declared outside-folder writes and process-lifetime folder approvals. | `09b6775` |
| 2026-10-31 | Removed filesystem confinement and folder grants; retained command approval. | Current change |
| 2026-11-05 | Added explicit verified-state delegation for a checked, committed, single-branch Git push and remote confirmation. | Current change |

## Inspect

- [Shell implementation](../internal/tools/bash.go), [configuration](../internal/config/config.go), and [tool tests](../internal/tools/test/tools_test.go).
- [Approval and write boundaries](write-approvals.md#write-boundaries).
- [Verified push implementation](../internal/delegation/push.go) and [local-repository coverage](../internal/delegation/test/push_test.go).

[Feature index](README.md)
