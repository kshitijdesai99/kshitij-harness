# Reference portability

Share reusable procedures and inert assets while preserving their provenance.

Introduced **2026-10-23** · commit `f802b27`.

## Current behavior

Use `kh memory reference put/get/export/import/asset` to manage database-native references. Export/import carries procedure content and provenance; import neither executes assets nor overwrites existing topics. `kh memory reference demo` stores a sound example without playing it.

References remain scoped, versioned knowledge rather than executable installations. Saving, retrieving, importing, and loading do not run scripts; any requested execution goes through normal approved, sandboxed tools. Consult the manual and CLI source for command details rather than treating a bundle as authority to act.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Added reference management, portable bundles, import provenance, inert assets, and the non-playing demonstration. | `f802b27` |
| 2026-10-23 | Added autonomous procedure learning as another way to populate versioned references. | `23e7a86` |

## Inspect

- Source: [reference CLI](../cmd/kh/reference.go), [reference storage and bundles](../internal/memory/references.go).
- Tests: [reference CLI](../cmd/kh/test/reference_test.go), [storage/import/export](../internal/memory/test/references_test.go).
- Manual: [procedures are inert references](../manual/memory.md#procedures-are-inert-references).

[Feature index](README.md)
