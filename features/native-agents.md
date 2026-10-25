# Native workers

Delegate independent tasks asynchronously while keeping parent and peer coordination visible.

Introduced **2026-09-29** · commit `277c243` (tmux sub-agent commands).

## Current behavior

- The native `agents` tool supports `spawn`, `send`, `peek`, and `list`; spawning returns immediately and opens a named tmux pane.
- Workers use addresses such as `kh:review` to report to their parent or coordinate directly with peers.
- Workers share project files, not isolated worktrees. There is no dependency scheduler; assign independent files or coordinate overlapping edits explicitly.
- Peer messages are attributed coordination, not human approvals or durable user instructions. Workers retrieve shared memory without learning delegated prompts as user instructions.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added shared-folder tmux sub-agent commands. | `277c243` |
| 2026-09-29 | Added named agent routing. | `14dd3e6` |
| 2026-10-04 | Added the native `agents` tool and coordinator implementation. | `c8de1bf` |
| 2026-10-16 | Excluded worker/peer prompts from user-facing memory learning. | `46c225b` |

## Inspect

[Coordinator](../cmd/kh/agents.go) · [Tool tests](../internal/tools/test/agents_test.go) · [Delegation manual](../manual/delegation.md#native-tmux-agents)

[Feature index](README.md)
