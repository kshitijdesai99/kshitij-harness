# kh

A lightweight, fast coding harness in Go. Two tools (`bash`, `edit`), Codex provider.

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
kh spawn tests "fix the failing tests"  # new agent/window, same working folder
kh send kh:main "done: 3 tests fixed" # type a line into another agent's chat
kh peek kh:tests                  # read recent screen lines
kh agents                        # list windows and busy/waiting state
```

A spawned agent stays in chat after its task, knows its parent's address, and is instructed to report back with `kh send`. Agents share files: give them independent files to edit. `kh peek` and `kh agents` are read-only and auto-approved; `spawn` and `send` ask unless `--auto` is on. Run `kh --auto` to pass auto-approval to agents you spawn.

## Config

Optional `~/.kh/config.json`; set only what you want to change:

```json
{ "model": "gpt-6-luna", "effort": "medium", "timeout_sec": 30, "output_cap": 20000, "map_cap": 20000 }
```

Also `web_search` (on by default), `system`, `safe`, `auto`, `sandbox` and `writable` (extra dirs bash may write to). Flags override the file.

Type while a task runs to steer it: a reply in progress is cut off and restarted with your line; a running command finishes first. In chat, `/model gpt-5.5` and `/effort high` switch from the next message and are saved to `~/.kh/config.json`, so every session (open ones too) follows; `/model` alone shows both.

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking. Everything else asks y/n unless `--auto`. On macOS, bash can only write inside the project (plus temp and cache dirs) unless `-nosandbox`.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how it works and [todo.md](todo.md) for what's next.
