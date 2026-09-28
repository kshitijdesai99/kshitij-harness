# kh todo

Rules: stdlib first, one file per job, comments say why, nothing extra. Design is in [ARCHITECTURE.md](ARCHITECTURE.md).

## Phases

- [x] 0. Skeleton
- [x] 1. Agent loop.
- [x] 2. Tools: `bash`, `edit`, parallel calls, `-y`.
- [x] 3. Repo map: files + Go symbols in the cached system prompt.
- [x] 4. Codex: device-code login, refresh with lock, Responses streaming.
- [x] 5. Sandbox: macOS `sandbox-exec` around `bash`; `-nosandbox` and `writable` to allow more.

## Later, if needed

- Linux sandbox (Landlock or bubblewrap)
- Repo map symbols for non-Go files (tree-sitter or ctags)
- Browser PKCE login for Codex (`localhost:1455`)
- `x-openai-internal-codex-residency` header (residency-locked workspaces)
- Start tools while the reply is still streaming
- `find_def`, LSP, graphify, fast search sub-model, MCP
