# Reference portability

Share reusable procedures and inert assets while preserving their provenance.

Introduced **2026-10-23** · commit `f802b27`.

## Current behavior

Use `kh memory reference put/get/export/import/asset` to manage database-native references. Export/import carries procedure content and provenance; import neither executes assets nor overwrites existing topics. `kh memory reference demo` stores a sound example without playing it.

References remain scoped, versioned knowledge rather than executable installations; the shared [procedure flow and inert-reference boundary](procedure-loading.md#procedure-flow) applies to bundles as well. Put/import populate the database reference directly; they do not run the learning extractor or execute content. Recall supplies compact hints, not the full body; explicit loading is a separate read. See [procedure learning](procedure-learning.md) for automatic creation and versioned improvements.

## Inspect and transfer an existing reference

Replace the paths, `my-procedure`, and `script.sh` with an existing reference's name (or numeric ID) and asset name. Run inspection/export in the source repository; run import from the destination repository so `repo` selects its scope.

```sh
cd /path/to/source-repo
kh memory reference get my-procedure
kh memory reference asset my-procedure script.sh
kh memory reference export my-procedure > /absolute/path/reference.json

cd /path/to/destination-repo
kh memory reference import repo < /absolute/path/reference.json
kh memory reference get my-procedure
```

`get` displays the procedure and inert assets; `asset` only prints source. These inspection and transfer commands do not execute scripts; execution is a separate, explicitly requested action under the [ordinary tool policy](write-approvals.md). Use `import global` instead of `import repo` only for a reference intended for all repositories. Import refuses to overwrite an existing topic.

The runtime database remains authoritative. Exported JSON is a transfer artifact, not a maintained documentation file or reference store.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-23 | Added reference management, portable bundles, import provenance, inert assets, and the non-playing demonstration. | `f802b27` |
| 2026-10-23 | Added autonomous procedure learning as another way to populate versioned references. | `23e7a86` |

## Inspect

- Source: [reference CLI](../cmd/kh/reference.go), [reference storage and bundles](../internal/memory/references.go).
- Tests: [reference CLI](../cmd/kh/test/reference_test.go), [storage/import/export](../internal/memory/test/references_test.go).

[Feature index](README.md)
