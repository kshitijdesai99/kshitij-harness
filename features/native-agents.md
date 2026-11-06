# Native agent ecosystem

Delegate independent tasks recursively, with one shared directory and capacity budget.

Introduced **2026-09-29** · commit `277c243` (tmux sub-agent commands).

## Current behavior

- The root session is a fixed manager. Descendants start as workers and can change their own role with `set_role` (`role: manager` or `worker`). Only managers can `spawn`; promotion enables recursive delegation without creating another capacity allowance.
- `spawn` takes `name` and either `task` or a structured `handoff`, and returns without waiting for completion. Ordinary spawning opens a named tmux pane; `reuse: true` first tries a compatible idle reusable worker, otherwise launches one. If assigning a selected idle worker fails, the error is returned without launching a replacement. Reuse retains the existing worker's name and address, so use the returned address rather than assuming the requested name identifies a newly created worker. Use `keep: true` with a plain task for a continuing interactive conversation instead; `keep` cannot be combined with `reuse` or a structured `handoff`. Descendants show the full assignment in a distinct [startup task banner](terminal-feedback.md#assigned-task-startup-banner) before model/tool work; it remains in scrollback, not pinned.
- `assign` takes `address` and a structured `handoff` to begin a **fresh task** on an idle reusable worker. Only the worker's parent manager can assign it. A new task gets a new provider/chat state rather than continuing the worker's previous conversation; see [eligibility and retirement](worker-lifecycle.md#task-fresh-reuse).
- `list` discovers the ecosystem directory; `stats` reports shared live-session, manager/worker and capacity counts. Use returned stable `kh:<id>` addresses for coordination. Short `kh:<name>` aliases resolve within the current ecosystem, not across unrelated roots.
- Any participant can `send` a single-line message to another agent in the same ecosystem, regardless of ownership branch or role. This is same-task steering or coordination, **not** a fresh assignment or conversation reset.
- `inspect` returns safe shared details, a first-line task summary capped at 280 runes, and bounded conclusions. `peek` is an alias for that safe view, not a capture of private pane output or full conversation history.
- `stop` takes an `address` and optional `scope: agent` (default) or `subtree`. See [lifecycle and capacity](worker-lifecycle.md) for stop accounting and safeguards.
- Workers share project files, not isolated worktrees. There is no dependency scheduler; assign independent files or coordinate overlapping edits explicitly.
- Peer messages are attributed and displayed distinctly from human input; they are not human approval responses and [do not submit or modify the user's draft](input.md#peer-messages-and-drafts). Workers retrieve shared memory without learning delegated prompts as user instructions; see [learning-origin filtering](learning-origin.md).
- All ecosystem pane/tab names show a [live descendant count and role](terminal-feedback.md#live-descendant-counts), including zero for leaf workers; see [worker lifecycle](worker-lifecycle.md) for successful/failed pane behavior, `keep`, and inherited policy.

## Visible worker results

The parent terminal observes an append-only result feed for its **direct children**, separate from the mutable latest conclusion returned by `inspect`. Task ID and generation identify reusable-worker completions; ordinary descendant turns use a stable publication ID. Retrying a publication does not create another result, while later tasks can publish identical text independently. Reassignment cannot overwrite an older feed entry before the parent reads it.

Each new result displays an always-visible **Task + Result** pair in the manager terminal, with no collapse control or new navigation. The task is the exact validated explicit task, captured separately with a 64 KiB bound and immutable for its lease/generation; legacy workers retain their immutable original launch assignment. Older records without that task display `Original task unavailable`, never a reconstruction from a task summary or private handoff. Terminal control sequences are sanitized for display.

`DisplayResultsAfter` is the presentation-only read API; its `Task *string` distinguishes a missing original task from an available one. Existing `Result`, `ResultsAfter`, and `agents inspect` retain their conclusion and safe-metadata contracts: they do not expose this task body or private worker context.

Published model conclusions contain only the latest assistant text, not conversation transcripts or tool history. They are bounded to 16,000 UTF-8 bytes with an ellipsis and truncation metadata. Failures publish a failure conclusion instead of reusing stale assistant text. Empty or whitespace-only sanitized conclusions are explicitly labeled `[no final conclusion]`; the sanitized body is preserved. The terminal emits the task and labeled [worker-result block](terminal-feedback.md#subdued-harness-output) together in one atomic, draft-safe write, without generating a model summary.

At process startup the parent captures the feed's high-water mark, then polls for later publications independently of model turns, steering, archival and human input. A live observer advances its cursor after displaying each result, including while idle. Registry read failures retain the cursor for a later retry. This is once-per-live-process display, **not historical replay or crash-atomic exactly-once delivery**: restarting or resuming intentionally skips existing publications. It does not inject a new model turn or automatically add the conclusion to model context; use `inspect` and explicit messages for coordination.

## Task handoffs

Pass a `handoff` object to `agents` `spawn` or `assign`. The CLI equivalents are `kh spawn --reuse --handoff NAME 'HANDOFF_JSON'` and `kh assign ADDRESS 'HANDOFF_JSON'`; plain `kh spawn --reuse NAME TASK` wraps the task in a version-1 handoff for the current directory. Structured spawning requires the handoff directory to match the launch directory.

A structured task handoff carries a complete objective, not the parent's conversation or a memory snapshot. The version-1 payload includes `task`, an existing canonical absolute `directory`, optional `scope`, `constraints`, `expected_output`, `foundation_id`, and `workflow`, plus `verification` with proposed `commands`, prior-evidence `references`, and optional `commit`, `branch`, and `upstream` identifiers. The directory and foundation identifier identify context, not authority; workers still inspect applicable [workspace guidance](project-configuration.md#target-workspaces).

Handoffs reject invalid or oversized input rather than silently truncating it: the task is limited to 64 KiB, other strings and list items to 4 KiB each, each list to 32 items, and aggregate raw field text to 128 KiB. Directory aliases containing symlinks or unnormalized path components are rejected. The prompt serializes the payload as escaped JSON inside an explicit delegated-data boundary. References and proposed checks are not proof of current state or permission to execute, and no transcript, secret, or automatically copied memory belongs in the handoff.

The normal workflow leaves `workflow` empty. The explicit `push_verified` workflow requires a full lowercase 40- or 64-hex commit ID, branch, upstream, and prior verification references; its live checks and failure boundaries belong to [verified Git push](shell.md#verified-git-push).

## Coordination diagram

![Human directs the root manager; a promoted child can spawn recursively. Agents share a registry and budget, not full histories.](../diagrams/agent-coordination.svg)

[Coordination SVG](../diagrams/agent-coordination.svg): solid arrows show direction or spawn, not completion; dashed arrows show messages; dotted lines show shared registry access. Ownership is a tree, but messaging, discovery and inspection are ecosystem-wide. Share compact conclusions and evidence deliberately; private conversation histories are not the coordination protocol. Shared files and memory are not security isolation.

## Human as CEO, main agent as manager

Keep direction and important choices with the human. Every current manager, including promoted descendants, plans, allocates execution to workers, coordinates their work, assesses evidence, resolves conflicts, and presents verified outcomes. Managers assign implementation and integration edits, routine tests, builds, installation and restart execution to workers, rather than performing that execution themselves. Finished workers do not transfer execution back to the manager: retain or reassign a worker or spawn another scoped worker. At full capacity, retain orchestration and wait, inspect or stop work before retrying; do not fall back to executing the task.

Managers delegate substantial investigation and source/document exploration. Once delegated, do not independently repeat broad exploration; use concise worker conclusions and targeted follow-ups first. Managers may answer ordinary informational questions directly, do initial scoping, and make narrow reads for specific missing evidence, conflicts or verification. Not every reply requires a worker. Workers execute their assigned scope, including integration and checks, and return conclusions, exact references, checks and uncertainty, not raw history or bulk source; they need not promote or delegate. On promotion, hand existing coding or execution tasks to workers before continuing. Do not switch roles to bypass delegation or become a worker while managing live descendants.

These role-conditional duties are supplied in universal runtime instructions to new and resumed roots and descendants, even with a custom system prompt. Successful native-tool promotion also returns a short manager reminder; CLI `set_role` keeps its JSON snapshot output. This is operating policy, not a hard tool prohibition: it does not add a deterministic scheduler or automatic runtime delegation. A useful request is:

> Split this change into independent tasks, delegate implementation and review, integrate the results, and report what you verified. Ask me only for decisions you cannot make safely.

### Duplicate manager exploration

The reported problem is a manager delegating investigation but then independently rereading broad source/document sets, duplicating worker effort and expanding the manager's context. The instruction-level fix is to consume compact worker evidence first and reserve direct reads for initial scoping or a specific evidence gap, conflict or verification need. Root/resume instructions, the native promotion reminder, the discoverable tool contract and repository guidance carry this policy. Regression tests check instruction delivery and the bounded-read exceptions, not deterministic model compliance; no hard read blocking or automatic scheduler is added.

The 2026-11-03 fix did not deliver central harness problem/feature recording: discovery then found no applicable local issue-recording operation, and external Jira/Cekura tools were not substitutes. It was left as separate outstanding work, not an implemented capability or a reason to write directly to the memory database.

## Integrate and verify

A handoff with `workflow: "push_verified"` uses the bounded [verified Git push](shell.md#verified-git-push) path without provider, memory-service or configured MCP startup. It is selected only explicitly; normal tasks still use the model/tool workflow.

1. Inspect the project and divide work by ownership or independent concern.
2. Give each worker a concrete outcome, scope, and verification requirement.
3. Collect concise conclusions, exact references, checks and uncertainty; use targeted follow-ups before direct reads and resolve overlapping changes before integration.
4. Assign relevant checks to workers, assess their evidence, inspect the combined diff, and state any remaining uncertainty.

The harness provides tools and visibility; the agent remains responsible for choosing and checking this workflow. Follow [development conventions](development.md) and the shared [command approval](write-approvals.md) and [shell execution guidance](shell.md). [External coding CLIs](external-clis.md) are shell subprocesses, not native workers.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added shared-folder tmux sub-agent commands. | `277c243` |
| 2026-09-29 | Added named agent routing. | `14dd3e6` |
| 2026-10-04 | Added the native `agents` tool and coordinator implementation. | `c8de1bf` |
| 2026-10-16 | Excluded worker/peer prompts from user-facing memory learning. | `46c225b` |
| 2026-10-25 | Added isolated peer transport and live receiver checks instead of simulated typing into the human draft. | `dee1c86` |
| 2026-10-31 | Added recursive roles, a shared registry/capacity budget, ecosystem discovery, safe inspection and stop controls. | Current change |
| 2026-11-01 | Made manager execution delegation explicit for roots and promoted descendants, with universal role-conditional instructions and a native promotion reminder. | Current change |
| 2026-11-03 | Extended manager delegation to substantial investigation and source/document exploration; prefer compact worker evidence over duplicate broad reads while retaining initial scoping and targeted verification. | Current change |
| 2026-11-05 | Added bounded versioned handoffs, explicit fresh-task assignment and opt-in compatible idle-worker reuse without parent history or automatic memory copying. | Current change |
| 2026-11-06 | Added bounded direct-child result publications and a live parent-terminal observer, without transcript sharing, historical replay or automatic model-context injection. | Current change |
| 2026-11-06 | Paired each visible result with its immutable original task through a presentation-only API, keeping coordination APIs conclusion-only. | Current change |

## Inspect

[Agents tool](../internal/tools/agents.go) · [Coordinator](../cmd/kh/agents.go) · [Runtime wiring](../cmd/kh/ecosystem.go) · [Registry](../internal/ecosystem/registry.go) · [Tool tests](../internal/tools/test/agents_test.go) · [Instruction delivery tests](../cmd/kh/test/manager_policy_test.go)

[Result storage](../internal/ecosystem/results.go) · [Live observer](../cmd/kh/ecosystem_results.go) · [Result-feed coverage](../internal/ecosystem/test/results_test.go) · [Idle/busy delivery and resume tests](../cmd/kh/test/completion_observer_test.go) · [Result renderer tests](../internal/terminal/test/agent_result_test.go)

[Assignment pairing](../internal/ecosystem/display.go) · [Immutable task capture tests](../internal/ecosystem/test/display_test.go) · [Paired renderer tests](../internal/terminal/test/agent_task_result_test.go)

[Handoff contract](../internal/delegation/handoff.go) · [Handoff coverage](../internal/delegation/test/handoff_test.go)

[Feature index](README.md)

## 2026-11-06 — Observe bounded direct-child results without transcript or model-turn injection

- Status: implemented
- Rationale: An append-only result feed preserves completions across worker reuse instead of polling an overwritten latest conclusion. A console-lifetime observer provides visibility without steering the model or replaying historical results.
- Affected: internal/ecosystem/results.go, internal/ecosystem/tasks.go, cmd/kh/ecosystem_results.go and publication wiring; see Visible worker results above.
- Verification / outstanding: Final `KH_AUTO_REBUILD=0 go test ./... -count=1`, focused renderer/feed/CLI checks and preview build passed on 2026-11-06. Manual terminal inspection remains unrun; external review is paused and incomplete.

## 2026-11-06 — Pair original tasks with visible worker results

- Status: implemented
- Rationale: Audit assignments against conclusions without extra navigation, model turns or private context exposure. Keep immutable explicit tasks separate from the conclusion-only coordination APIs; never reconstruct missing originals.
- Affected: Result storage, presentation-only DisplayResultsAfter, parent observer and terminal paired renderer; current behavior and evolution above.
- Verification / outstanding: Final `KH_AUTO_REBUILD=0 go test ./... -count=1` passed on 2026-11-06. Focused terminal/PTY tests (28.579s), completion-observer/startup-banner CLI tests (5.382s), ecosystem store tests (0.435s) and preview build passed. Manual visual inspection remains unrun; external review is paused and incomplete.
