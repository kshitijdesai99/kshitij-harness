# New and forked chats

Explore work in parallel without replacing the original conversation.

Introduced **2026-10-05** · commit `388e256` (`/fork`).

## Current behavior

- `/new` opens a fresh chat in a right-hand tmux pane; `/fork` opens a copy of the last completed conversation checkpoint there.
- Forking does not steer or interrupt the original task. Partial replies and unfinished tool calls are excluded.
- New and forked chats follow [approval-policy inheritance](write-approvals.md). Parallel chats share project files; a conversation fork is not an isolated worktree.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Added `/fork`, initially opening a new tmux window. | `388e256` |
| 2026-10-16 | Moved forks to right panes and allowed forking without steering active work. | `7cf70cf` |
| 2026-10-16 | Added `/new` for fresh right-pane chats. | `49b0277` |

## Inspect

[Chat lifecycle](../cmd/kh/chat.go) · [Fork tests](../cmd/kh/test/fork_test.go) · [Saved sessions](saved-sessions.md) · [Live tmux chats](tmux-chats.md)

[Feature index](README.md)
