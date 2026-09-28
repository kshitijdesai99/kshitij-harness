# kh

A lightweight, fast coding harness in Go. Two tools (`bash`, `edit`), Codex provider.

## Setup

```bash
go build -o kh ./cmd/kh
./kh login codex        # ChatGPT login, no API key
```

## Use

```bash
./kh "fix the failing test in foo_test.go"
./kh -y "run go test and fix errors"   # skip y/n prompts
./kh -model gpt-5.5 -effort high "hi"
```

## Config

Optional `~/.kh/config.json`; set only what you want to change:

```json
{ "model": "gpt-6-luna", "effort": "medium", "timeout_sec": 120, "output_cap": 20000, "map_cap": 20000 }
```

Also `system`, `safe` and `yes`. Flags override the file.

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking. Everything else asks y/n unless `-y`.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how it works and [todo.md](todo.md) for what's next.
