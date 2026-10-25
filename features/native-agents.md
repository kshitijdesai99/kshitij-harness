# Native workers

Delegate independent tasks asynchronously while keeping parent and peer coordination visible.

Introduced **2026-09-29** · commit `277c243` (tmux sub-agent commands).

## Current behavior

- The native `agents` tool supports `spawn`, `send`, `peek`, and `list`; spawning returns immediately and opens a named tmux pane.
- Workers use addresses such as `kh:review` to report to their parent or coordinate directly with peers.
- Workers share project files, not isolated worktrees. There is no dependency scheduler; assign independent files or coordinate overlapping edits explicitly.
- Peer messages are attributed and displayed distinctly from human input; they are not human approval responses and [do not submit or modify the user's draft](input.md#peer-messages-and-drafts). Workers retrieve shared memory without learning delegated prompts as user instructions; see [learning-origin filtering](learning-origin.md).
- See [worker lifecycle](worker-lifecycle.md) for successful/failed pane behavior, `keep`, and inherited policy.

## Human as CEO, main agent as manager

Keep direction and important choices with the human. The main agent should plan, delegate, oversee evidence, resolve conflicts, and present the result. It may do integration or focused implementation itself; delegation reduces cognitive burden rather than forbidding the manager from touching code.

This is guidance, not a deterministic scheduler or a promise that every task launches workers. A useful request is:

> Split this change into independent tasks, delegate implementation and review, integrate the results, and report what you verified. Ask me only for decisions you cannot make safely.

## Integrate and verify

1. Inspect the project and divide work by ownership or independent concern.
2. Give each worker a concrete outcome, scope, and verification requirement.
3. Collect findings and resolve overlapping changes before integration.
4. Run relevant checks, review the combined diff, and state any remaining uncertainty.

The harness provides tools and visibility; the agent remains responsible for choosing and checking this workflow. Follow [development conventions](development.md) and the shared [approval](write-approvals.md) and [shell execution boundaries](shell-sandbox.md). [External coding CLIs](external-clis.md) are shell subprocesses, not native workers.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added shared-folder tmux sub-agent commands. | `277c243` |
| 2026-09-29 | Added named agent routing. | `14dd3e6` |
| 2026-10-04 | Added the native `agents` tool and coordinator implementation. | `c8de1bf` |
| 2026-10-16 | Excluded worker/peer prompts from user-facing memory learning. | `46c225b` |
| 2026-10-25 | Added isolated peer transport and live receiver checks instead of simulated typing into the human draft. | `dee1c86` |

## Inspect

[Agents tool](../internal/tools/agents.go) · [Coordinator](../cmd/kh/agents.go) · [Tool tests](../internal/tools/test/agents_test.go)

[Feature index](README.md)
