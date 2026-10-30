# Shell sandbox (retired)

Introduced **2026-09-28** · commit `83bb8cd`. Removed **2026-10-31**.

kh no longer confines filesystem access to the launch folder or maintains folder grants. Shell commands and structured edits can access any location permitted by the operating system. [Command approval](write-approvals.md) remains available through `/auto` and CLI settings; it is not filesystem confinement.

See [shell commands](shell.md) for current execution behavior and [target workspaces](project-configuration.md#target-workspaces) for inspecting another folder before work.

[Feature index](README.md)
