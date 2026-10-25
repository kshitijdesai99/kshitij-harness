# Edit path protections

Keep structured edits within their authorized destinations, including approved outside folders.

Introduced **2026-09-28** · commit `84d64c8` (initial project-path check; stronger protections followed).

## Current behavior

The `edit` tool uses canonical, pinned access and symlink checks rather than relying only on a textual path prefix. Outside-project edits request a folder grant under the active approval policy. Configured shell-writable directories are not implicit edit grants. Authorized access does not establish that the proposed content change is safe.

The edit gate coordinates calls within one process, not independent agents sharing files. [Structured editing](editing.md) documents batch validation and partial-write behavior.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial edit tool rejected destinations outside the project lexically. | `84d64c8` |
| 2026-10-01 | Added rooted access and same-open-file writes to prevent path redirection. | `60e2bd1` |
| 2026-10-04 | Serialized edit handling within each process. | `c8de1bf` |
| 2026-10-16 | Extended pinned access to approved outside folders. | `09b6775` |

## Inspect

- [Edit implementation](../internal/tools/edit.go) and [pinned access](../internal/tools/access.go)
- [Edit access tests](../internal/tools/test/edit_access_test.go) and [batch tests](../internal/tools/test/edit_batch_test.go)
- [Write boundaries](../manual/safety-and-sessions.md#approval-and-write-boundaries)

[Feature index](README.md)
