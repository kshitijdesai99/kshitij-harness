# Version history and provenance

Explain how retained knowledge changed without silently replacing earlier versions.

Introduced **2026-10-05** · commit `0a203a6` (append-only history).

## Current behavior

- `kh memory get ID` inspects a version; `kh memory history repo KEY` inspects a topic’s history. The model tool also exposes get/history.
- Notes retain discussion context, statement/save timestamps, importance (`normal`, `critical`, or empty for legacy unclassified rows), optional absolute expiry, and links to replaced versions. Expected-version and statement-time checks reject stale writes, including concurrent-agent updates.
- The newest row for each `(repo, key)` is current; expiry does not resurrect a superseded version. Expired rows remain available through get/history but are ineligible for routine recall. Tier changes preserve version provenance rather than rewriting history. Generated summary rows carry a whole-source revision fingerprint, so stale generated summaries are not injected.
- [Forgetting](memory-controls.md) preserves earlier versions; history explains saved knowledge, not every attempted extraction or all conversation activity.
- A persistent high-water allocator keeps new-code note IDs from being reused after purge, including across reopened stores and concurrent writers. Its singleton stores only the greatest allocated ID, not deleted note content; an insert trigger also observes IDs written by older binaries. It cannot reconstruct IDs purged before migration, and already-running older writers can still reuse IDs themselves. Restart all writers onto the new build for the no-reuse guarantee.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Added append-only versions, history inspection, and expected-version checks. | `0a203a6` |
| 2026-10-16 | Added discussion context, statement timestamps, replacement links, and richer history filters. | `46c225b` |
| 2026-10-17 | Guarded background updates against misapplied topic changes. | `77639f2` |

## Inspect

[Store](../internal/memory/memory.go) · [Provenance tests](../internal/memory/test/provenance_test.go) · [Database design](knowledge-store.md)

[Feature index](README.md)


## 2026-11-11 — Preserve note identity after purge

- Status: implemented
- Rationale: An Add/Purge/Add regression demonstrated reused note IDs, which can corrupt seen tracking and provenance identity. Use an additive metadata-only notes_sequence high-water singleton with an insert trigger rather than retaining purged content or rebuilding the notes table. New-code allocation is transactional across stores; purge never lowers the watermark. Older writers can still reuse IDs themselves, and pre-migration purged identities cannot be reconstructed, so all writers must restart for the guarantee.
- Affected: internal/memory/hierarchy.go and memory.go; note/reference writes; identity regression tests; memory-history.md and knowledge-store.md
- Verification / outstanding: Purge/reopen/reference/legacy-insert and concurrent two-store allocation regressions passed, along with the final normal suite, vet and memory race checks. Independent storage review found no remaining demonstrated defects. See [combined verification and race scope](keyword-recall.md). The old-writer and pre-migration identity limitations remain explicit above.
