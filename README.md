# kh

A lightweight, fast coding harness in Go. Native shell, edit, memory, and tmux-agent tools, a backend-neutral core, and a Codex adapter.

## Setup

```bash
go build -o kh ./cmd/kh
./kh login codex        # ChatGPT login, no API key
```

Rebuilding `./kh` does not update an installed `kh` elsewhere on `PATH` or an already-running chat. Launch the rebuilt executable explicitly (`./kh -s SESSION_ID` to resume), or use `go install ./cmd/kh` to update the installed command and then restart it. Check `command -v kh` if a bare `kh` still behaves like an older build.

Once installed, use **`kh --rebuild`** to rebuild and atomically replace the exact executable you invoked (including through a symlink). It uses local sources, first checking the current checkout and then its embedded build-source path; use `KH_SOURCE_DIR=/path/to/kshitij-harness kh --rebuild` if the checkout moved or the binary was built with `-trimpath`. Go must be installed, and the executable directory must be writable. Build failures leave the old executable untouched. The command exits without starting or restarting a chat; exit your current chat and run `kh -r` to load the new build. It does not pull changes, commit, or push.

## Use

```bash
./kh --help                              # show CLI usage
./kh --help how do i load a session       # kh-aware one-off answer: no session opened or saved
./kh --rebuild                           # rebuild this executable from local sources, then exit
./kh                                    # chat: keep typing, Ctrl-C stops a task, Ctrl-D quits
./kh -r                                 # continue this folder's last session; shows the earlier chat first
./kh --sessions                         # list this folder's 20 most recently saved chats with last-response previews (alias: kh sessions)
./kh -s 20260928-194501.123             # continue a specific one (ids in ~/.kh/sessions)
./kh "fix the failing test in foo_test.go"
./kh -i "fix the failing test"          # do the task, then stay in chat
./kh --auto "run go test and fix errors"  # skip y/n prompts
./kh -model gpt-5.5 -effort high "hi"
./kh -nosandbox "update ~/.zshrc"      # allow writes outside the project
```

`kh --help REQUEST` (also `-h REQUEST`) streams one answer and exits. It assumes questions about sessions, models, and commands refer to kh, and includes kh's command reference and the running executable's flag usage. Explicitly unrelated questions are still supported. It uses your configured provider/model and login, but does not load or save a chat, open an interactive console/tmux pane, access stored memory, or execute local shell/edit tools. Configured built-in web search remains available. Quote the request if it contains shell-special characters; model/effort flags must come before the request. It cannot be combined with `-r`, `-s`, `-i`, or `--sessions`. Bare `kh --help` displays usage without needing valid chat configuration or login.

## Input history

Session listings show local last-saved time, session ID, and a preview of the most recent assistant message. Rows fit the detected stdout terminal width; redirected output uses a valid `COLUMNS` value or defaults to 120 columns. Previews use at most 70 display columns including the ellipsis, preserve Unicode grapheme clusters, and strip terminal controls. In narrow panes the timestamp is omitted and the preview shrinks; IDs remain exact unless the pane itself is narrower than the ID. Chats without an assistant response are labeled explicitly. Last-saved time is the session file's modification time, not an exact response-generation timestamp; `kh -r` still resumes the newest session by creation ID.

New sessions are saved in `~/.kh/sessions/v2/<SHA-256 of canonical working-folder path>/`. This avoids collisions between distinct paths that contained separators and hyphens. Existing chats in the old flattened folder layout are still listed and loadable; resaving writes to the new namespace without moving or deleting the legacy file. New saves take precedence when an ID exists in both locations. Already-ambiguous legacy folder keys cannot be assigned to an original project automatically.

Queries are cyan and assistant responses are green in interactive terminals, including replayed sessions. Redirected output stays plain; set `NO_COLOR=1` to disable chat colors.

In interactive chat, press **Up** to recall your previous query and **Down** to move toward newer entries or restore your unfinished draft. Recalled text is editable; press Enter to submit it. History keeps up to 500 entries for the current process only (it is not saved across restarts). Blank lines and `y`/`yes`/`n`/`no` approval answers are excluded. Ctrl-C still stops the active task, and Ctrl-D on an empty line quits. Piped input remains line-based unless it includes explicit bracketed-paste boundaries.

Terminal pastes are buffered as **one block**, shown as `[paste <id>: N lines]`. Pasted newlines do not submit messages: add any surrounding text, then press **Enter** to send the complete block with its line breaks preserved. Up recalls its placeholder, so the block can be resent without turning into individual queries. Multiline blocks containing `/model`, `/effort`, or `/image` are message content, not batches of slash commands. This uses bracketed-paste framing supported by modern terminals and tmux; no timing-based guessing. Each paste is limited to 4 MiB, with 16 MiB retained across paste history; an evicted block asks you to paste it again rather than silently losing content.

## Working indicator

As soon as a turn starts, the pane/tab title animates with a spinner, current phase, and elapsed seconds—for example `main / waiting for model (8s)`. Phases include responding, running a tool, waiting for approval, and saving the session. After 20 seconds without a phase/output update, a `quiet Ns` marker makes the wait explicit. A moving spinner means the UI is responsive, **not proof that the remote model is making progress**. Completion, errors, and Ctrl-C restore the normal pane name.

The indicator uses title updates rather than cursor redraws, so it does not overwrite drafts or streamed responses. Redirected output and dumb terminals have no animation. Inside tmux, the pane's top border shows the title; addresses remain stable regardless of animated titles.

## Batch edits

The existing `edit` tool accepts either `{path, old, new}` or a coherent batch of up to 128 changes:

```json
{
  "edits": [
    {"path": "main.go", "old": "old name", "new": "new name"},
    {"path": "main.go", "old": "old condition", "new": "new condition"},
    {"path": "test/main_test.go", "old": "old expectation", "new": "new expectation"}
  ]
}
```

Changes apply in array order to staged text, so a later replacement can match text introduced earlier. Empty `old` creates a new file; subsequent entries can edit that staged file. All replacements and paths are validated before writes, then each file is written once, preserving existing permissions and project-root containment. Validation errors change nothing, including no new directories. This is **not a cross-file filesystem transaction**: cancellation or an I/O failure during writing reports completed files; the failing file may also be partially written. Edit calls are serialized within each harness process to prevent lost updates; separate agents still need distinct file ownership. Prefer batches over tiny edit/check cycles, and group related reads, formatting, and focused tests.

## Images

In an interactive tmux chat on macOS, copy an image and press **Ctrl-V** in a `kh` pane (in any tmux session, including split panes). kh reads and validates the clipboard **at Ctrl-V**, then inserts a short visible placeholder; add any text and press Enter to send the captured image. If the clipboard contains only text, kh shows a pasteboard-type diagnostic immediately and leaves your input unchanged. The captured image is held in a private temporary cache file until sent; an unsent image may leave a cache file behind. The server-wide tmux key binding checks the active pane and passes Ctrl-V through unchanged in non-`kh` panes; an existing user Ctrl-V binding is never replaced. If your terminal does not forward Ctrl-V or you already bind it in tmux, type `/image` to attach the current clipboard image, then type your message. The clipboard reader uses Swift/AppKit. You can also type `/image path/to/image.png` on any platform to attach a PNG, JPEG, WebP, or GIF file (maximum 10 MB). One image can be attached per message. Images are included in saved sessions; reopening a session displays `[image attached]` instead of printing image data.

## Tmux agents

Agent orchestration is a native model tool (`agents`), not a shell-command recipe. Ask kh to open a tests/review/docs agent: it chooses a purpose-based name and immediately opens a pane **on the right of the current window**. Main stays on the left; additional workers stack in the right column. Your current tab and focus are preserved. Creation does not wait for the worker's model response.

The native tool supports `spawn` (name/task, optional keep), `send` (address/message), `peek` (address), and `list`. Main and workers can communicate directly with each other at `kh:main` and `kh:<name>`; messages carry the sender's address. These are logical addresses within the current tmux session, so this also works when you start kh inside your own tmux session. Names survive pane moves and joins.

Interactive `kh` starts or attaches to a tmux session named `kh` when launched outside tmux. Install tmux first; one-shot tasks and piped input don't require it. The CLI remains available:

```bash
kh spawn tester "run the failing tests"    # right-hand pane, same working folder
kh spawn --keep reviewer "review the tester's proposed fix"  # stays open for follow-ups
kh send kh:reviewer "tester found an edge case in parser.go"
kh peek kh:tester                     # read recent screen lines
kh agents                            # list addresses and busy/waiting state
```

A spawned agent does its task, reports back using the native tool, and exits, so tmux closes its pane. If the task fails, it stays in chat so the error stays visible. Use `--keep` (or `keep` in the tool) to leave a worker open for follow-up messages. Each agent knows its own and parent's addresses. Spawning and messaging through this tool require no separate shell approval; bash commands inside each worker still obey the inherited approval and sandbox settings. Agents inherit the active provider/model/effort without another login. They share files: assign independent files and coordinate before editing the same file. If the screen is too small for another pane, spawning reports the tmux error rather than hiding the worker in another tab.

## Memory

One local SQLite database (`~/.kh/memory.db`) holds every repo's memory in one append-only table—no Markdown memory files. Each row is an **instruction** (a rule you set), a **gotcha** (a trap kh found while working), or the repo's **summary**. A change adds a new row with the same key; the newest row per repo and key is the current version, and older rows stay as history. Repos are named by their normalized Git remote (credentials stripped), so moved folders and other clones share memory; without a remote, the Git root or folder path is used. `global` rows apply to every repo.

kh saves memory by itself in the background with a cheap model (`memory_model`), one job at a time per process:

- **Pre-hook**, on each message: saves lasting rules you state ("from now on…"), reusing an existing key to replace a rule. It reads only your words.
- **Post-hook**, after a turn: runs only when a tool call failed and a later call of the same tool succeeded, and saves a durable gotcha from that evidence. A gotcha never replaces an instruction.
- **Summary**, at the end of a turn when a note changed: a short overview plus the keys that must always load, rebuilt from current notes (never from the old summary) and trimmed in code to `summary_max_tokens`.

Hooks save only against the version they were shown, so a slow or stale decision is dropped rather than overwriting a newer change. Text that looks like a secret is refused. Each save prints a `(memory)` line between turns, so a wrong one can be forgotten.

Before each message, kh sends one memory message just before it: the summary (once per chat), new versions of notes already sent, and the top `top_instructions` instructions and `top_gotchas` gotchas by keyword search (SQLite FTS5) over current rows only. Notes are labelled `[memory #id]` and saved in the chat, so each is sent once; compacting drops them, and they are sent again. Memory is reference data: your latest message wins.

```bash
kh memory list                                      # summary and current notes for this repo and global
kh memory search 'css styling'                      # matching instructions and gotchas
kh memory history repo css-framework                # every version of one topic
kh memory remember repo instruction css-framework 'Use plain CSS.'
kh memory forget repo css-framework                 # stop using it; history kept
kh memory purge repo css-framework                  # delete every version
kh memory import-md AGENTS.md                       # one-time: split a rules file into notes, after review
```

The `memory` tool lets the model search, and remember or forget when you ask. On first open, records from the earlier discovery/detail tables move into the new table. The database lives outside Git and is private to the local user.

## Config

Optional `~/.kh/config.json`; set only what you want to change:

```json
{ "provider": "codex", "model": "gpt-6.1-sol", "effort": "medium", "model_idle_timeout_sec": 120, "timeout_sec": 30, "output_cap": 20000, "map_cap": 0 }
```

Also `web_search` (on by default), `system`, `safe`, `auto`, `sandbox` and `writable` (extra dirs bash may write to). Memory: `memory_model` (cheap model for the background hooks; empty uses the chat model, `"off"` disables automatic saving), `summary_max_tokens` (2500, estimated as characters ÷ 4), `top_instructions` and `top_gotchas` (10 each). Flags override the file.

Model connections time out after 120 seconds without headers or streamed bytes; `model_idle_timeout_sec` changes that silence limit (zero or negative uses 120). This is not a total generation timeout: incoming bytes keep an otherwise healthy request alive. Dropped connections, truncated streams before visible output, and transient HTTP failures retry after one second, with a one-minute recovery budget that also bounds retried requests. A server's longer `Retry-After` is respected. Retry notices explain what happened; Ctrl-C cancels requests and retry waits. Once reply text or a web-search action has been displayed, a broken stream fails rather than replaying and duplicating output. Partial local tool calls are discarded and never executed. Authentication lock acquisition and token refresh share a 30-second deadline and respect cancellation; single-use token refreshes are not automatically retried. Rebuild and restart existing chats to load this behavior; changing the silence setting also requires a restart.

Use `/compact` between turns to summarize the conversation with the current model and save the shorter context for resume. This makes a model request but executes no tools. Failed, cancelled, empty, or non-shrinking summaries leave history unchanged. Compaction replaces detailed messages, tool logs, and images with a text handoff; important details can be lost. Restart with the rebuilt executable to use the command.

Type while a task runs to steer it: a reply in progress is cut off and restarted with your line; a running command finishes first. In chat, `/model gpt-5.5` and `/effort high` switch from the next message and are saved to `~/.kh/config.json`, so every session (open ones too) follows; `/model` alone shows both.

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking when their syntax and options are recognized. Write/execute options and unsupported syntax require approval. This conservative allowlist is not a shell security boundary; custom `safe` entries are trusted configuration. Everything else asks y/n unless `--auto`. On macOS, bash can only write inside the project (plus temp and cache dirs) unless `-nosandbox`. On other Unix platforms, kh warns that the OS sandbox is unavailable; approval checks still apply, but are not an OS security boundary. The implementation requires Unix process/locking APIs and `/bin/bash`; Windows is not currently supported. Interactive tmux features require tmux, and clipboard-image capture is macOS-only (local image paths remain usable). Command output is bounded while it is collected, not only when it is sent to the model; truncation drops partial UTF-8 characters at cut boundaries.

## Architecture

- `cmd/kh`: CLI setup and one chat's lifecycle.
- `internal/agent`: orchestration against a small `Stepper` interface, independent of model APIs.
- `internal/provider`: backend-neutral contracts, optional capabilities, and cancellation/rollback requirements. Replay callbacks are presentation fragments; semantic last-response previews use a separate optional capability.
- `internal/backend`: provider selection and login dispatch, isolated from the provider contract. Capability-specific instructions are added only when the corresponding tool is available.
- `internal/provider/codex`: all Codex implementation code—client/protocol, authentication, stream parsing, retries, compaction—and its `test/` suite. Only Codex is currently implemented.
- `internal/terminal`: per-chat input/history, cancellable approvals, and rendering. Adapters emit text, not terminal colors.
- `internal/tools`: tool schemas and behavior; bash receives an approval interface instead of using global input.
- `internal/memory`: the append-only notes table, keyword search, repo identity, secret filter, and building each message's memory.
- `internal/learn`: the background pre-hook, post-hook, and summary rebuild, using any provider without tools.
- `internal/session`: collision-resistant per-folder namespaces and atomic saves of versioned, backend-tagged opaque state. Legacy storage locations and pre-envelope Codex chats still resume.

`provider` selects the API/auth adapter; `model` selects a model within that backend. `-provider codex` overrides the configured backend. Resuming uses the session's recorded backend; an explicit conflicting `-provider` is rejected. Switching backends starts a new chat rather than reinterpreting another provider's history. Spawned agents inherit the active provider, model, and effort, including command-line overrides.

To add a backend, implement `provider.Provider`, emit through the injected `provider.Output`, and add its constructor/login route in `internal/backend/backend.go`. The loop, tools, UI, and session storage do not need backend-specific branches. Images, retrieved memory, compaction, and semantic last-response previews are explicit optional capabilities. Every adapter must honor context cancellation promptly and discard partial failed responses/unpaired tool calls before returning; add conformance tests for these guarantees. Keep credentials, request formats, streaming events, and private history inside the adapter; do not infer a backend from a model-name prefix.

The memory design is drawn in [memory-architecture.png](memory-architecture.png).
