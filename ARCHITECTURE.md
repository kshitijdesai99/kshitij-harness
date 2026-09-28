# Architecture

```
cmd/kh/main.go            loads config, applies flags, builds tool list
internal/config/          defaults + ~/.kh/config.json
internal/repomap/         files + top-level symbols for the system prompt
internal/agent/loop.go    the loop
internal/provider/        Provider interface, codex.go
internal/tools/           Tool struct, bash.go, edit.go, test/
internal/auth/codex.go    ChatGPT device-code login + token refresh
```

## Config

Built-in defaults, then `~/.kh/config.json` (only the fields it sets), then flags (`-model`, `-effort`, `-y`). A missing file is fine.

| Key | Default |
|---|---|
| `model` | `gpt-6-luna` |
| `effort` | `medium` |
| `system` | short fixed prompt |
| `timeout_sec` | `120` |
| `output_cap` | `20000` |
| `map_cap` | `20000` (0 = off) |
| `safe` | `rg grep cat head tail ls wc sed find pwd file tree`, `git status/diff/log/show` |
| `yes` | `false` |

OpenAI URLs, the client id, headers and login timings are constants in code, not config, because changing them breaks the protocol.

## Loop

`agent.Run` calls `Provider.Step(task)`, runs the returned tool calls in parallel (one goroutine each, results kept in order), and calls `Step(results)` again. It stops when a step returns no calls.

## Plugins

- `tools.Tool` is a struct: name, description, JSON Schema params, `Run(ctx, input) (string, error)`. A new tool is one file plus one entry in the list in `main.go`.
- `provider.Provider` is one method: `Step(ctx, user, results) ([]Call, error)`. Each provider owns its history in its own wire format, so the loop stays format-agnostic. It streams text to stdout.

## Providers

**Codex** (`codex.go`): raw HTTP + SSE to `chatgpt.com/backend-api/codex/responses`.
- `store: false`, so the full `input` history is resent each turn. Items come from `response.output_item.done` and are replayed with `id` removed (stored ids 404).
- `include: reasoning.encrypted_content` keeps reasoning across turns without server storage.
- `reasoning.effort` from config.
- `prompt_cache_key` = per-run session id, so turns hit the same cache.
- Headers: `Authorization`, `ChatGPT-Account-ID` (from JWT), `originator: kh`, `session_id`.

## Auth (Codex)

Same flow as Hermes (`NousResearch/hermes-agent`), public Codex client id `app_EMoamEEZ73f0CkXaXp7hrann`:
1. `POST auth.openai.com/api/accounts/deviceauth/usercode` gives a user code.
2. User enters it at `auth.openai.com/codex/device`; we poll `/deviceauth/token` (403/404 = pending).
3. Exchange code + verifier at `/oauth/token`.

Tokens live in `~/.kh/codex.json` (0600), separate from `~/.codex` because refresh tokens are single-use. `auth.Token` takes an exclusive `flock`, refreshes 2 min before JWT `exp`, and re-reads inside the lock so concurrent runs never reuse a spent refresh token.

## Tools

**bash**: `/bin/bash --noprofile --norc -c` (starts in ~4 ms). Own process group, `timeout_sec` kills the whole group, stdin is `/dev/null`. Output capped to `output_cap` (first + last half). Failed commands return output + exit error as a normal result, so the model can react.
Commands matching `safe` run without asking, piped together; an entry like `git diff` matches leading words. Always unsafe, whatever the config: `; & > < $ \`` or newline, `sed -i`, `find -exec|-execdir|-delete|-ok`. Prompts are serialised with a mutex. `-y` skips them.

**edit**: `old` must appear exactly once, then replace. Empty `old` creates the file (`O_EXCL`, fails if it exists). Paths must resolve inside the project.

## Repo map

Built once at start and appended to the system prompt, so it is part of the cached prefix. Files come from `git ls-files --cached --others --exclude-standard` (tracked + new, not ignored); if that is empty or fails, walk the dir skipping dot dirs, `node_modules`, `vendor`. `.go` files get their top-level types, vars, funcs and `Type.Method`s via `go/parser`; other files are listed by path. Stops at `map_cap` bytes, which also bounds the time spent (~65 ms for 20 KB of Go's own source tree).

## Speed choices

- Short, fixed system prompt: fewer tokens and a stable cache prefix.
- Two tools only: smaller tool schema, and the model is already fluent in shell.
- Parallel tool execution; prompt tells the model to batch reads.
- Find-and-replace edits instead of whole-file rewrites (fewer output tokens).
- Output caps keep context small.
- Repo map up front, so the model often opens the right file without searching.
- Stdlib only. Single static binary.

## Not yet

`sandbox-exec` (phase 5). Until phase 5, `bash` can touch anything the user can, so the y/n prompt is the only guard.
