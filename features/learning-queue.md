# Background learning queue and summaries

Bound automatic retention work so learning does not block the chat answer.

Introduced **2026-10-05** · commit `0a203a6`.

## Current behavior

When automatic learning is enabled, one worker processes a bounded 32-job queue serially. Each job has a cancellable two-minute context; a full queue drops work with a notice rather than blocking chat. Extraction uses fresh tool-less model calls with bounded evidence/context to interpret lessons without treating a whole transcript as instructions, and can decline or fail. Notices are displayed between turns, not a complete audit of every attempt.

Summaries rebuild from canonical current repo/global notes, never by summarizing an earlier gist. The result is a whole contextual synopsis, not a substitute for exact critical rules. Its bounded source listing prioritizes critical notes and then fairly samples newest notes across kinds/scopes, reporting omitted source counts. A fingerprint covers the entire active canonical snapshot, including omitted notes, not merely selected keys or the highest ID. Edits, tier changes, expiry, forgetting, purging, and global changes invalidate it. Saving checks both the source fingerprint and expected summary revision atomically; recall suppresses stale generated summaries until a later background check rebuilds them. The synopsis is admitted whole only when fresh and within its allowance, not cut into misleading fragments.

Startup queues catch-up work; shutdown allows up to 30 seconds for queued work before cancellation. [Hook scheduling](learning-hooks.md) does not wait for a save, and separate processes still need [store revision checks](memory-history.md) to avoid stale updates.

### Importance and legacy classification

New durable notes receive a conservative normal/critical classification. Routine follow-ups preserve existing tier and expiry unless the current user's own words support a metadata change. Expiry requires a literal absolute RFC3339 deadline or explicit removal; event timestamps, quoted text, assistant/tool suggestions, and peer messages are not authority to invent one.

Legacy empty tiers are classified lazily in bounded background batches, only for current unexpired notes with recognized user-facing source provenance, owner, statement time, and discussion context. Imports, references, summaries, peers, and unsupported provenance do not bootstrap as new user instructions. Classification appends a CAS-guarded metadata-only version, preserving the original statement and discussion provenance; it is not fresh user endorsement. Invalid output or concurrent changes leave the canonical note intact. With automatic saving off, this background classification and synopsis work do not run.

## Queue in context

See the [learning flow](learning-hooks.md#learning-flow) for enqueue events, full-queue drops, extraction, and guarded saves. Summary checks are separate jobs, not an extraction output or an unconditional consequence of a save. Startup also queues a catch-up check; extraction can decline or fail, and any queued job can be dropped when the queue is full.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Introduced serial background hooks, bounded work, notices, and summaries rebuilt from current notes. | `0a203a6` |
| 2026-10-16 | Expanded selective extraction with discussion context and event timestamps. | `46c225b` |
| 2026-10-17 | Added guarded memory-update decisions to prevent weak or unsupported replacements. | `77639f2` |
| 2026-10-23 | Added eligible procedure extraction to completion jobs. | `23e7a86` |

## Inspect

- Source: [queue and summaries](../internal/learn/learn.go), [update guards](../internal/learn/update.go), [startup/shutdown](../cmd/kh/main.go).
- Tests: [learning and summaries](../internal/learn/test/learn_test.go), [selectivity and queue behavior](../internal/learn/test/selective_test.go), [updates](../internal/learn/test/update_test.go), [classification and source freshness](../internal/learn/test/hierarchy_test.go).

[Feature index](README.md)


## 2026-11-11 — Conservative tier classification and source-fresh contextual synopsis

- Status: implemented
- Rationale: Classify durable notes without turning routine follow-ups into unsupported tier or expiry changes. Lazily classify only provenance-supported legacy user-facing notes using metadata-only CAS versions. Rebuild a whole contextual synopsis from canonical notes, prioritize critical inputs fairly within a bounded listing, and fingerprint the entire active source snapshot so omissions, global edits, expiry and purge cannot leave a stale gist eligible for injection. Exact critical rules remain independent; model output is not guaranteed.
- Affected: internal/learn/learn.go, hierarchy.go and update.go; internal/memory/hierarchy.go; learning-queue.md; linked classification/freshness tests
- Verification / outstanding: Final normal suite, vet and applicable memory/learning race checks passed. Independent storage/learning review verified explicit expiry renewal, omitted-expiry non-renewal, metadata guards, bounded bootstrap, summary freshness and close/requeue behavior with no remaining demonstrated defects. See [combined verification and race scope](keyword-recall.md). Classification and synopsis generation remain selective, nonauthoritative and model-dependent.
