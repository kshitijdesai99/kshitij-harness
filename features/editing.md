# Structured edits

Make bounded file changes with explicit replacements and validated batches.

Introduced 2026-09-28 · commit `84d64c8`.

## Current behavior

Use `edit` for exact-match replacements, new files, or ordered multi-file batches. Replacement text must match exactly once; an empty `old` creates a new file. Batch replacements run in order against staged text, with validation before writes and each file written once. Validation errors write nothing, but an I/O failure can leave earlier files updated: batches are not rollback transactions. [Edit path protections](edit-path-safety.md) describe destination checks; approval policy still applies.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Introduced exact-match replacements and new-file creation. | `84d64c8` |
| 2026-10-04 | Added ordered, prevalidated multi-file edit batches. | `c8de1bf` |
| 2026-10-16 | Integrated process-lifetime approval for outside-folder edits. | `09b6775` |

## Inspect

- [Edit implementation](../internal/tools/edit.go), [batch tests](../internal/tools/test/edit_batch_test.go), and [access tests](../internal/tools/test/edit_access_test.go).
- [Approval and write boundaries](../manual/safety-and-sessions.md#approval-and-write-boundaries).

[Feature index](README.md)
