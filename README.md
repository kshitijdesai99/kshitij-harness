# kh

A lightweight, fast coding harness in Go. Three tools (`bash`, `edit`, `memory`), Codex provider.

## Setup

```bash
go build -o kh ./cmd/kh
./kh login codex        # ChatGPT login, no API key
```

## Use

```bash
./kh                                    # chat: keep typing, Ctrl-C stops a task, Ctrl-D quits
./kh -r                                 # continue this folder's last session; shows the earlier chat first
./kh sessions                           # list this folder's sessions with their first message
./kh -s 20260928-194501.123             # continue a specific one (ids in ~/.kh/sessions)
./kh "fix the failing test in foo_test.go"
./kh -i "fix the failing test"          # do the task, then stay in chat
./kh --auto "run go test and fix errors"  # skip y/n prompts
./kh -model gpt-5.5 -effort high "hi"
./kh -nosandbox "update ~/.zshrc"      # allow writes outside the project
```

## Tmux agents

Interactive `kh` starts or attaches to a tmux session named `kh` (its first window is `kh:main`). Inside tmux it runs normally. Install tmux first; one-shot tasks and piped input don't require tmux.

```bash
kh spawn tester "run the failing tests"    # new agent/window, same working folder
kh spawn reviewer "review the tester's proposed fix"
kh send kh:reviewer "tester found an edge case in parser.go"  # message by agent name
kh peek kh:tester                    # read recent screen lines
kh agents                            # list named agents and busy/waiting state
```

A spawned agent stays in chat after its task, knows its parent's address, and is instructed to report back with `kh send`. Agent names are unique within the `kh` session and belong to panes: after moving an agent into a split pane, `kh send kh:reviewer ...`, `kh peek kh:reviewer`, and `kh agents` still work by name. Pane titles display those names when pane borders are enabled. Agents can message each other directly using `kh send`, without routing messages through the parent. Agents share files: give them independent files to edit. `kh peek` and `kh agents` are read-only and auto-approved; `spawn` and `send` ask unless `--auto` is on. Run `kh --auto` to pass auto-approval to agents you spawn.

## Memory

One local SQLite database (`~/.kh/memory.db`) holds durable preferences, workflows, and project facts—no skill files. `discovery` stores short searchable metadata, and `detail` stores the full text linked by foreign key. At each turn, kh searches discovery records for this repo and global preferences, loads at most three relevant details (bounded to 1,400 characters), and supplies them as ephemeral context before the user message—without storing them in session history or bloating the stable system prompt. The `memory` tool supports search/get/remember/forget on demand. Explicitly requested lasting memories replace records with the same scope and key; the current request always wins. Do not store secrets.

```bash
kh memory list                            # short discovery entries for this repo and global scope
kh memory search 'review tmux'            # search discovery metadata
kh memory get 1                           # load one linked detail
kh memory remember repo review workflow 'Visible review' 'Show both reviewers on the right' 'Run Claude above Codex and verify direct messaging.' 'claude codex tmux'
kh memory forget repo review              # delete discovery and detail together
```

Use `global` instead of `repo` for preferences that apply across projects. The harness reads/writes the DB itself, so agents do not need to curate files. The database lives outside Git and is private to the local user.

## Config

Optional `~/.kh/config.json`; set only what you want to change:

```json
{ "model": "gpt-6-luna", "effort": "medium", "timeout_sec": 30, "output_cap": 20000, "map_cap": 0 }
```

Also `web_search` (on by default), `system`, `safe`, `auto`, `sandbox` and `writable` (extra dirs bash may write to). Flags override the file.

Type while a task runs to steer it: a reply in progress is cut off and restarted with your line; a running command finishes first. In chat, `/model gpt-5.5` and `/effort high` switch from the next message and are saved to `~/.kh/config.json`, so every session (open ones too) follows; `/model` alone shows both.

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking. Everything else asks y/n unless `--auto`. On macOS, bash can only write inside the project (plus temp and cache dirs) unless `-nosandbox`.

Architecture is discoverable with `kh memory search architecture` and `kh memory get <id>`; see [todo.md](todo.md) for future ideas.
