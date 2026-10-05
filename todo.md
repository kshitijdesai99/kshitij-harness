# kh todo

Rules: stdlib first where practical, one file per job, comments say why, nothing extra. Design is indexed in the memory database (`kh memory search architecture`).

## Phases

- [x] 0. Skeleton
- [x] 1. Agent loop.
- [x] 2. Tools: `bash`, `edit`, parallel calls, `--auto`.
- [x] 3. Repo map: optional files + Go symbols (disabled by default for a small system prompt).
- [x] 4. Codex: device-code login, refresh with lock, Responses streaming.
- [x] 5. Sandbox: macOS `sandbox-exec` around `bash`; `-nosandbox` and `writable` to allow more.
- [x] 6. Chat mode and resumable sessions (`-r`, `-s`).
