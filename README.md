# kh

A terminal coding assistant in Go: chat, run shell commands, edit files, and delegate work to native tmux agents.

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

On macOS, shell reads are allowed wherever OS permissions permit; writes are sandboxed to the project, configured tool/cache directories, and folders granted for the running session. Outside-folder edits and declared shell `write_dirs` are automatically approved unless auto is disabled. `/auto on` persists `"auto": true`; `--auto` enables it for one session. This keeps sandbox and pinned-path checks; it does not grant the whole filesystem at startup. Workers and new/forked chats inherit the active policy, not directory grants. `/auto off` restores command and new-folder prompts; existing grants remain until restart. `--nosandbox` disables shell write restrictions. Other Unix systems have no OS-level shell sandbox; approvals alone are not a security boundary.

## Memory and agents

kh selectively saves lasting instructions, established facts, accepted decisions, and gotchas in `~/.kh/memory.db`; it does not archive every discussion into memory. Notes keep discussion context, statement/save timestamps, and links to the versions they replace. Repo and global scopes remain separate. Delegated workers read shared memory; user-facing chats learn from exchanges. Use `kh memory status`, `search`, `get`, or `history` to inspect them; `kh memory explain "query"` shows retrieval candidates and fallback status. Saved chats live separately in `~/.kh/sessions`.

Search uses keywords by default, with no model downloads. Optional hybrid mode requires Ollama and Python 3.10+ with venv support. `/memory hybrid on` explicitly downloads and verifies Qwen3-Embedding 0.6B and Qwen3-Reranker 0.6B before enabling hybrid retrieval; `/memory hybrid off` immediately restores keyword-only retrieval without deleting models or vectors. Open chats pick up mode changes on their next message. Optional `memory_embedding_url`, `memory_embedding_model`, and `memory_reranker_url` settings override the local defaults. Startup only uses cached models; embedding or reranking failures fall back to keywords with a notice. `memory_max_tokens` bounds injected memory (estimated as characters/4); context and history are fetched on demand. Managed runtime files live in `~/.kh/hybrid`, which remains writable under the shell sandbox without granting access to config or login files.

If hybrid retrieval falls back, the notice identifies the embedding or reranking stage. `kh memory explain "query"` reports which stages fell back; `/memory hybrid on` retries setup and verification. Reranking waits up to 16 seconds for busy workers and allows 8 seconds for inference, within a 25-second request budget. Keyword-only mode needs neither service.

Chat notices show compact stage counts, loaded memory names, and retrieval time. `/memory explain` displays the last retrieval’s full IDs and scores without searching again. Common standalone greetings, check-ins, and acknowledgments skip topic search, as does knowledge already present in the chat; corrections, required summary notes, and explicit named references still load. Successful reranking can return no relevant memories rather than filling quotas with weak matches.

Managed rerankers authenticate their helper revision. Normal startup never replaces a different build’s helper; `/memory hybrid on` explicitly upgrades it gracefully. Scoring requests reject a mismatched revision instead of silently using the wrong prompt. Unknown or legacy helpers without an ownership handshake are never terminated automatically; the error explains the one-time manual stop needed before adopting the managed lifecycle.

Named procedural references keep full procedures and inert script assets in the same database. Names, aliases, and topics retrieve compact hints; the memory tool's `load` action fetches the full item on demand. `kh memory reference demo` stores a sound example without playing it. Use `reference put/get/export/import/asset` to manage individual versioned references; imports never execute assets or overwrite existing topics. Execute requested assets only through normal approved, sandboxed commands—not during retrieval.

See the [memory architecture](memory-architecture.svg) for retrieval, saving, and reference-loading flows.

Ask kh to delegate work using native agents. Workers share project files: assign separate files or coordinate edits. Their tmux panes close after successful tasks; failures stay visible, and `keep` retains a worker for follow-ups.
