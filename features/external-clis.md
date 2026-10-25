# External coding CLIs

Use other installed coding tools as shell subprocesses when useful.

Introduced **2026-09-28** · commit `84d64c8` (initial shell execution, not a separate native integration).

## Current behavior

- Invoke installed CLIs such as Claude Code through `bash`. Each requires its own installation, login, and suitable environment.
- Commands remain subject to [approval and write boundaries](write-approvals.md#write-boundaries) and the [shell’s write policy](shell-sandbox.md). Long-running work may need background execution followed by status checks; the [coordinator](native-agents.md#integrate-and-verify) must inspect results and verify changes rather than trusting a subprocess’s success message.
- This is not a native Claude provider, MCP client, or managed external-agent protocol. Codex is the only implemented native model adapter.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial `bash` tool could execute installed commands, including external CLIs. | `84d64c8` |
| 2026-09-28 | Added macOS shell write sandboxing. | `83bb8cd` |
| 2026-10-23 | Documented the external-CLI integration boundary and verification responsibilities. | `23e7a86` |

## Inspect

[Shell implementation](../internal/tools/bash.go) · [Shell tests](../internal/tools/test/tools_test.go)

[Feature index](README.md)
