# kh

A lightweight coding harness with agent delegation and a database-backed memory architecture for continuous learning and relevant context retrieval.

You set the direction; the main agent coordinates tasks, delegates to native tmux agents or external coding tools such as Claude Code, and verifies results. The human-as-CEO, agent-as-manager philosophy keeps routine coordination off your plate while allowing the main agent to implement and integrate changes when useful.

kh provides chat, shell commands, file edits, shared memory, and agent coordination. The manager model is the working philosophy, not a guarantee of automatic delegation. See the [manual](manual/README.md) for implemented behavior and design decisions.

![kh terminal showing rendered Markdown responses](sample.png)

## Setup

Requires Go 1.24+ and Unix with `/bin/bash`. Install tmux for interactive chat; one-shot tasks do not require it.

From this checkout:

```bash
go build -o kh ./cmd/kh
./kh login codex                 # ChatGPT login; no API key
```

## Use

```bash
./kh                            # interactive chat
./kh "fix the failing tests"     # task, then stay in chat
./kh -p "fix the failing tests"  # headless task, then exit
./kh -r                         # resume this folder's latest session
./kh --sessions                 # list saved sessions
./kh -s SESSION_ID              # resume a specific session
```

Outside tmux, interactive launches create a fresh, uniquely named tmux session; existing chats keep running. Use `tmux list-sessions` and `tmux attach -t SESSION_NAME` to reconnect to a running chat. `-r` and `-s` resume saved conversations in a new chat.

**Enter** sends; **Shift+Enter** starts a new line (shown with a `... ` continuation prompt) without sending. Earlier lines stay in the draft until Enter sends the whole message. **Ctrl-J** also starts a new line if your terminal cannot distinguish Shift+Enter from Enter.

**Ctrl-C** discards the draft and stops a task; **Ctrl-D** on an empty line quits. **Up/Down** recall and navigate input history. Type while kh works to steer it.

Run `./kh --help` for CLI options; use `/help` in chat for commands. Ask kh to inspect a local image with the `view_image` tool (PNG, JPEG, WebP or GIF, up to 10 MB), including files outside the repository. It sends the image itself to the active model. You can also attach a local image with `/image path/to/image.png`, or use `/image` for the macOS clipboard (requires Swift).

## Updates

Session startup automatically rebuilds the invoked executable from local sources and runs the new build. This requires Go, the kh source checkout, and a writable executable directory. Set `KH_SOURCE_DIR=/path/to/kshitij-harness` if sources moved, or `KH_AUTO_REBUILD=0` to skip rebuilding. Failed builds stop startup without replacing the old binary. Existing running chats are unchanged; restart and resume to use updates.

See [release notes](release-notes/) for recent changes, grouped by date.

## Configuration and safety

Optionally create `~/.kh/config.json`, specifying only overrides:

```json
{ "effort": "high" }
```

CLI flags override configuration. Automatic command and requested folder-write approval is on by default. Use `--auto=false` for approval prompts in one session, or `/auto off` to persist that preference. `-i` remains a compatibility alias for interactive mode.

On macOS, shell writes are sandboxed to the project, configured tool/cache directories, and session-approved folders; automatic approval does not grant the entire filesystem. Other Unix systems have no OS-level shell sandbox, so approvals alone are not a security boundary. See [safety and sessions](manual/safety-and-sessions.md) for write grants, worker policy, and sandbox controls.

## Memory and agents

kh aims to learn from every conversation while selectively saving useful memories, not archiving every discussion. Post-query/pre-answer and post-response hooks queue background learning in user-facing chats; delegated workers only retrieve shared memory. Reliable, well-referenced retrieval is the foundation for useful intelligence: applicable notes with provenance and on-demand detail matter more than collecting more notes. Runtime knowledge lives in `~/.kh/memory.db`, separate from saved chats in `~/.kh/sessions` and these explanatory docs. Use `kh memory status`, `search`, `get`, or `history` to inspect it; `kh memory explain "query"` shows retrieval candidates and fallback status.

Search uses keywords by default, with no model downloads. Optional `/memory hybrid on` sets up verified local embedding and reranking models; it requires Ollama and Python 3.10+ with venv support. `/memory hybrid off` restores keyword-only retrieval. Failures fall back to keywords with a notice; `/memory explain` shows the last retrieval’s diagnostic details.

Background learning also recognizes useful workflows demonstrated by successful tool results and saves them as named procedures without a review prompt. Full procedures and inert script assets live in the database; compact hints are retrieved first, and the memory tool's `load` action fetches the full item on demand. Saving or loading never executes them. See [memory and procedures](manual/memory.md) for controls, provenance, and hybrid retrieval details.

Ask kh to delegate work using native agents. Workers share project files: assign separate files or coordinate edits. Their tmux panes close after successful tasks; failures stay visible, and `keep` retains a worker for follow-ups. Internal agent messages appear in amber, distinct from your query color. External coding CLIs run through shell tools, not a native MCP integration. See [delegation and tools](manual/delegation.md).

## Manual

- [Architecture](manual/architecture.md): turn loop, providers, and presentation.
- [Memory and procedures](manual/memory.md): database-backed knowledge and retrieval.
- [Delegation and tools](manual/delegation.md): manager workflow, workers, and external tools.
- [Safety and sessions](manual/safety-and-sessions.md): approvals, sandbox boundaries, resume, and updates.
