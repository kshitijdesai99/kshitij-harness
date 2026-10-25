# Shell write sandbox

Restrict shell writes without treating execution permission as proof of safety.

Introduced **2026-09-28** · commit `83bb8cd`.

## Current behavior

On macOS, shell write sandboxing is enabled by default. Writable locations include the project, configured tool/cache directories, and session-approved folders; outside writes are declared with `write_dirs`. Reads still obey OS permissions. `--nosandbox` disables shell restrictions. Other Unix systems have no equivalent OS-level shell write boundary; approval prompts alone are not one. Automatic approval does not disable the sandbox or authorize the whole filesystem.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added the macOS shell write sandbox. | `83bb8cd` |
| 2026-10-01 | Hardened command execution and sandbox handling. | `60e2bd1` |
| 2026-10-10 | Preserved sandboxed access to the memory store. | `56b393b` |
| 2026-10-16 | Added declared outside-folder writes backed by session grants. | `09b6775` |

## Inspect

- [Shell implementation](../internal/tools/bash.go)
- [Sandbox tests](../internal/tools/test/sandbox_test.go) and [memory-access cases](../internal/tools/test/sandbox_memory_test.go)
- [Approval and write boundaries](../manual/safety-and-sessions.md#approval-and-write-boundaries)

[Feature index](README.md)
