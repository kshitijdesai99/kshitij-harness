# Learning origin filtering

Keep delegated tasks and peer coordination from becoming durable user instructions.

Introduced **2026-10-16** · commit `46c225b`.

## Current behavior

Startup creates a learner only in user-facing chats with [automatic saving enabled](memory-controls.md). Workers still retrieve shared memory but do not learn their delegated task prompts as user instructions. The learner rejects peer-message inputs, including mixed user/peer composites.

Once a run includes peer coordination, its tool evidence, final reply, and recent learning context are excluded from post-response learning, even if a user steers later. Separate clean user queries still receive the [pre-hook](learning-hooks.md). This conservative boundary deliberately forgoes some learning to avoid attributing worker directives to the user.

## Origin fence in context

The [learning flow](learning-hooks.md#learning-flow) distinguishes query eligibility from completion eligibility. A peer-containing composite skips query extraction; any peer contribution makes the whole run ineligible for post-response learning. That fence also excludes recent learning context after failure or cancellation, without stopping shared recall. Cancellation excludes the completion hook, not query work already queued before it.

## Host memory provenance

Recall's seen tracking is distinct from learning eligibility. Only host-created memory items receive a private persisted `kh_memory` marker in provider history. It survives save/resume, is stripped from model-wire input, and is removed from streamed provider items rather than accepting a provider-supplied spoof. User text, tool results, and note text cannot establish host origin by copying a memory header. Legacy untagged saved memories are conservatively treated as unseen and may be reinjected within the normal budget. Compaction and failed-turn cleanup retain the provider's existing history lifecycle.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-16 | Disabled worker learning at startup and rejected messages beginning with a peer marker. | `46c225b` |
| 2026-10-23 | Detected peer markers throughout composite input and added a sticky run-origin fence for completion evidence and recent context. | `23e7a86` |

## Inspect

- Source: [startup boundary](../cmd/kh/main.go), [peer-input checks](../internal/learn/learn.go), [run origin](../internal/learn/origin.go), [chat wiring](../cmd/kh/chat.go), [provider history provenance](../internal/provider/codex/client.go), [seen tracking](../internal/memory/recall.go).
- Tests: [origin fencing](../internal/learn/test/origin_test.go), [selective learning](../internal/learn/test/selective_test.go), [host-origin persistence and spoof resistance](../internal/provider/codex/test/memory_origin_test.go).

[Feature index](README.md)


## 2026-11-11 — Host-only persisted memory origin for seen tracking

- Status: implemented
- Rationale: Memory-looking strings are not host provenance. Persist a private kh_memory tag only on host-created provider history items, strip it from model-wire input and incoming streamed items, and trust it for recall seen tracking. Conservatively reinject bounded notes from legacy untagged histories rather than allowing user/tool/note text to suppress recall. Keep this separate from user-facing learning eligibility.
- Affected: internal/provider/codex/client.go and stream.go; internal/memory/recall.go; learning-origin.md; provider memory-origin tests
- Verification / outstanding: Provider persistence, spoof, resume, compaction and failure/replay coverage passed with the final normal suite, vet and applicable provider/memory race checks. Independent review found no remaining demonstrated defects. See [combined verification and race scope](keyword-recall.md). Existing history lifecycle is preserved.
