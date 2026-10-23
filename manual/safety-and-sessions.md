# Safety and sessions

What kh enforces, where protection ends, and how to continue work safely.

## Approval and write boundaries

Automatic command and requested folder-write approval is **on by default**. `--auto=false` requires prompts for one session; `/auto off` persists that preference. `/auto on` persists automatic approval; `--auto` enables it for one session. Flags override configuration.

On macOS, the [shell sandbox](../internal/tools/bash.go) allows reads subject to OS permissions and restricts writes to the project, configured tool/cache directories, and session-approved folders. Shell calls declare outside write folders with `write_dirs`; edits request outside-folder approval automatically. Auto approval retains sandbox and pinned-path checks; it does not grant the entire filesystem at startup.

Turning auto off restores command and new-folder prompts, but existing grants remain until restart. Workers and new/forked chats inherit active policy, not directory grants. `--nosandbox` disables shell write restrictions. Other Unix systems have no OS-level shell sandbox; approval prompts alone are not a security boundary.

These are execution controls, not proof that a command is safe or that model output is correct. External CLIs run under the same shell policy, with their own behavior and credentials. Retrieved memories and procedure assets are reference data, never authorization to execute.

## Steering and stopping

Typing a new message during a reply steers the model: kh cancels the partial reply, waits for provider cleanup, and submits the new message. Already-running tools finish before steering takes effect so results remain paired with calls. Steering is not a rollback of filesystem changes.

**Ctrl-C** stops a task or discards the draft; it cannot undo completed side effects. **Ctrl-D** on an empty input quits. See the [README](../README.md#use) for multiline input and image controls.

## Saved history versus running chats

[Sessions](../internal/session/session.go) are scoped to the working folder, with symlinks resolved. The application saves provider-owned history after turns under `~/.kh/sessions`; the memory database is separate.

```bash
kh --sessions       # this folder’s saved chats
kh -r               # newest by creation ID, not last save time
kh -s SESSION_ID    # resume a specific saved conversation
```

Resume replays saved history in a new chat; it does not reattach a running process. Interactive launches outside tmux create fresh, uniquely named tmux sessions. Use `tmux list-sessions` and `tmux attach -t SESSION_NAME` to reconnect to one already running.

`/new` opens a fresh chat in another pane. `/fork` opens the last completed conversation in another pane without interrupting the original task; partial replies and unfinished tool calls are excluded. Both inherit active policy, not session-only directory grants.

## Updates and restart

Normal session startup [rebuilds the invoked executable](../cmd/kh/rebuild.go) from local sources and runs it. This needs Go, the checkout, and a writable executable directory. Set `KH_SOURCE_DIR=/path/to/kshitij-harness` if sources move, or `KH_AUTO_REBUILD=0` to skip rebuilding. Failed builds stop startup without replacing the old binary. Utility commands do not trigger this rebuild.

Existing chats keep their running build. To use an update, restart and resume the saved session; rebuilding alone does not replace a live process. `kh --rebuild` rebuilds and exits. See [release notes](../release-notes/) for changes.
