# Features

Use this index to see what kh does, when each capability first appeared, and what followed. Each feature has one owning page for operating instructions, limitations, evidence, and dated development history—no separate manual to maintain.

**Introduced** means the first implementation in Git, not the date its documentation was written. “What followed” is a short signpost, not a complete changelog; dates and commit references live on the linked page. Baseline: `dee1c86`, inspected October 25, 2026. Source/test links are evidence, not a claim of fresh passing tests.

Shared guides: [Architecture](architecture.md) · [Development](development.md) · [Maintenance](CONTRIBUTING.md)

## Execution and context

| Feature | Introduced | What followed |
|---|---|---|
| [Execution loop](execution.md) | 2026-09-28 | Steering, provider separation, user-message hooks |
| [Shell commands](shell.md) | 2026-09-28 | Sandbox, output handling, outside-folder grants |
| [Structured editing](editing.md) | 2026-09-28 | Validated batches and cross-folder edits |
| [Web search](web-search.md) | 2026-09-28 | Visible search/open/find actions |
| [Repository map](repository-map.md) | 2026-09-28 | Git-only scope; now opt-in |

## Interactive control

| Feature | Introduced | What followed |
|---|---|---|
| [Chat and headless modes](chat-modes.md) | 2026-09-28 | Interactive default and explicit `-p` mode |
| [Steering and stopping](steering.md) | 2026-09-28 | Reply interruption and safer cleanup |
| [Input composition and history](input.md) | 2026-10-01 | Multiline drafts and isolated peer delivery |
| [Model and effort settings](model-settings.md) | 2026-09-28 | Persistent settings across chats |
| [Help](help.md) | 2026-09-28 | Contextual, tool-less one-off help |

## Sessions and branches

| Feature | Introduced | What followed |
|---|---|---|
| [Saved sessions](saved-sessions.md) | 2026-09-28 | Replay, previews, provider-owned checkpoints |
| [Running tmux chats](tmux-chats.md) | 2026-09-29 | Fresh independent sessions on launch |
| [Chat branches](chat-branches.md) | 2026-10-05 | Right-pane forks and `/new` |
| [Context compaction](compaction.md) | 2026-10-04 | Tool-less enforcement, feedback, persistence fixes |

## Delegation

| Feature | Introduced | What followed |
|---|---|---|
| [Native agents](native-agents.md) | 2026-09-29 | Named routing, native tool, isolated peer transport |
| [Worker lifecycle and policy](worker-lifecycle.md) | 2026-09-29 | Auto-closing panes and unified approval policy |
| [External coding CLIs](external-clis.md) | 2026-09-28 | Documented coordination through shell tools |

## Memory and learning

| Feature | Introduced | What followed |
|---|---|---|
| [Knowledge store](knowledge-store.md) | 2026-09-29 | Append-only typed notes |
| [Memory history and provenance](memory-history.md) | 2026-10-05 | Discussion provenance and stale-update checks |
| [Memory inspection and forgetting](memory-controls.md) | 2026-09-29 | Version inspection and scoped filters |
| [Learning hooks](learning-hooks.md) | 2026-10-05 | Steering coverage and selective extraction |
| [Learning queue and summaries](learning-queue.md) | 2026-10-05 | Context-aware extraction, update guards, procedure jobs |
| [Learning-origin protections](learning-origin.md) | 2026-10-16 | Mixed-peer input and whole-run exclusion |

## Retrieval and procedures

| Feature | Introduced | What followed |
|---|---|---|
| [Keyword recall](keyword-recall.md) | 2026-09-29 | Bounded context and less redundant recall |
| [Hybrid retrieval](hybrid-retrieval.md) | 2026-10-16 | Managed local models, [runtime](hybrid-runtime.md), and applicability scoring |
| [Retrieval diagnostics](retrieval-diagnostics.md) | 2026-10-23 | Candidate scores, rejection and fallback details |
| [Procedure learning](procedure-learning.md) | 2026-10-23 | No later changes at baseline |
| [Procedure loading](procedure-loading.md) | 2026-10-23 | Relevance diagnostics and autonomous population |
| [Procedure portability](procedure-portability.md) | 2026-10-23 | Autonomous learning populates shareable references |

## Safety

| Feature | Introduced | What followed |
|---|---|---|
| [Shell sandbox](shell-sandbox.md) | 2026-09-28 | Approved outside folders and memory/runtime access |
| [Write approvals](write-approvals.md) | 2026-09-28 | Session grants and unified auto policy |
| [Edit path safety](edit-path-safety.md) | 2026-09-28 | Pinned paths and cross-root validation |

## Presentation and visual input

| Feature | Introduced | What followed |
|---|---|---|
| [Markdown and math](markdown.md) | 2026-10-05 | Distinct commentary/stats and unified tool styling |
| [Terminal feedback](terminal-feedback.md) | 2026-10-01 | Activity indicators, commentary, peer colors, URLs |
| [Turn metrics](turn-metrics.md) | 2026-09-28 | Thinking/context metrics and corrected TTFT |
| [Images](images.md) | 2026-09-30 | Encoding cleanup and local image-viewing tool |
| [Visual companion prototype](visual-companion.md) | 2026-10-29 (initial shipping change) | Standalone example; no later changes yet |

## Reliability and updates

| Feature | Introduced | What followed |
|---|---|---|
| [Provider state and boundaries](provider-state.md) | 2026-09-28 | Backend-neutral contract and failure cleanup |
| [Authentication and connection recovery](connection-recovery.md) | 2026-09-28 | Retry/idle handling and expired-token recovery |
| [Rebuild and updates](startup-rebuild.md) | 2026-10-04 | Automatic rebuild/re-exec at startup |
| [Managed hybrid runtime](hybrid-runtime.md) | 2026-10-23 | Bounded contention and revision-aware helpers |

## Using and maintaining this catalog

- For how the systems connect, see [architecture](architecture.md); for setup, see the [project README](../README.md); for implementation/test conventions, see [development](development.md).
- For recent changes across features, see [dated release notes](../release-notes/). Feature pages keep the history of their own capability.
- Follow the [maintenance guide](CONTRIBUTING.md) when adding, changing, splitting, replacing, or retiring a feature. Keep detailed history out of this index.
