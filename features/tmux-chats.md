# Running tmux chats

Keep independent interactive chats alive and reconnect to their running processes.

Introduced **2026-09-29** · commit `277c243`.

## Current behavior

- Interactive terminal launches outside tmux create fresh, uniquely named tmux sessions. Tmux is required for that launch path; launches already inside tmux stay there.
- Inspect running sessions with `tmux list-sessions`; reconnect using `tmux attach -t SESSION_NAME`.
- A running process is distinct from [saved history](saved-sessions.md): `-r` and `-s ID` resume a conversation in a new process, not the original live chat. Attaching does not change its build; follow [restart onto a new build](startup-rebuild.md#restart-onto-a-new-build) to update a live chat.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added tmux chat attachment and shared-folder sub-agent support. | `277c243` |
| 2026-10-16 | Replaced reuse of the fixed tmux session with fresh interactive sessions. | `fc43dfb` |

## Inspect

[Tmux launch](../cmd/kh/tmux.go) · [Tests](../cmd/kh/test/tmux_test.go) · [New and forked chats](chat-branches.md)

[Feature index](README.md)
