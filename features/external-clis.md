# External coding CLIs

Use other installed coding tools as shell subprocesses when useful.

Introduced **2026-09-28** · commit `84d64c8` (initial shell execution, not a separate native integration).

## Current behavior

- Invoke installed CLIs such as Claude Code through `bash`. Each requires its own installation, login, and suitable environment.
- Commands remain subject to [command approval](write-approvals.md). kh imposes no filesystem confinement; subprocesses run subject to OS permissions and any controls of their own. Long-running work may need background execution followed by status checks; the [coordinator](native-agents.md#integrate-and-verify) must inspect results and verify changes rather than trusting a subprocess’s success message.
- External CLI invocation is not a native Claude provider or managed external-agent protocol. Codex is the only implemented native model adapter. [Project configuration](project-configuration.md) separately supports `.agents`/`.claude` configuration and MCP tools, resources, and prompts.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial `bash` tool could execute installed commands, including external CLIs. | `84d64c8` |
| 2026-09-28 | Added macOS shell write sandboxing. | `83bb8cd` |
| 2026-10-31 | Removed kh filesystem confinement for shell subprocesses. | Current change |
| 2026-10-23 | Documented the external-CLI integration boundary and verification responsibilities. | `23e7a86` |

## Inspect

[Shell implementation](../internal/tools/bash.go) · [Shell tests](../internal/tools/test/tools_test.go)

[Feature index](README.md)
