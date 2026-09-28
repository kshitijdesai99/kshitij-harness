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
./kh -model gpt-5.4 "hi"
```

Read-only commands (`rg`, `cat`, `ls`, `git diff`, ...) run without asking. Everything else asks y/n unless `-y`.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how it works and [todo.md](todo.md) for what's next.
