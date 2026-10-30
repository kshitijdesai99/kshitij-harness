# Edit path protections

Keep structured edits attached to the intended files without restricting them to a project folder.

Introduced **2026-09-28** · commit `84d64c8` (initial project-path check; stronger protections followed).

## Current behavior

The `edit` tool can edit files anywhere permitted by the operating system, without folder approval. It pins existing files to open descriptors and validates parent paths to prevent pathname changes from redirecting a write. Symlink and hardlink aliases share staged text and one final write. These integrity checks are not a filesystem access boundary and do not establish that the proposed content change is safe.

The edit gate coordinates calls within one process, not independent agents sharing files. [Structured editing](editing.md) documents batch validation and partial-write behavior.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial edit tool rejected destinations outside the project lexically. | `84d64c8` |
| 2026-10-01 | Added rooted access and same-open-file writes to prevent path redirection. | `60e2bd1` |
| 2026-10-04 | Serialized edit handling within each process. | `c8de1bf` |
| 2026-10-16 | Extended pinned access to approved outside folders. | `09b6775` |
| 2026-10-31 | Removed folder approvals while retaining pinned-file integrity checks. | Current change |

## Inspect

- [Edit implementation and pinned file access](../internal/tools/edit.go)
- [Edit access tests](../internal/tools/test/edit_cross_directory_test.go) and [batch tests](../internal/tools/test/edit_batch_test.go)
- [Filesystem access and command approval](write-approvals.md#write-boundaries)

[Feature index](README.md)
