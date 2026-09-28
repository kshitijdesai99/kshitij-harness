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
./kh -r                                 # continue the last session; shows the earlier chat first
./kh sessions                           # list saved sessions with their first message
./kh -s 20260928-194501.123             # continue a specific one (ids in ~/.kh/sessions)
./kh "fix the failing test in foo_test.go"
./kh -i "fix the failing test"          # do the task, then stay in chat
./kh -y "run go test and fix errors"   # skip y/n prompts
./kh -model gpt-5.5 -effort high "hi"
./kh -nosandbox "update ~/.zshrc"      # allow writes outside the project
```

## Config

Optional `~/.kh/config.json`; set only what you want to change:

```json
{ "model": "gpt-6-luna", "effort": "medium", "timeout_sec": 30, "output_cap": 20000, "map_cap": 20000 }
```

Also `web_search` (on by default), `system`, `safe`, `yes`, `sandbox` and `writable` (extra dirs bash may write to). Flags override the file.

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking. Everything else asks y/n unless `-y`. On macOS, bash can only write inside the project (plus temp and cache dirs) unless `-nosandbox`.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how it works and [todo.md](todo.md) for what's next.
