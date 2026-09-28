# Architecture

```
cmd/kh/main.go            flags, picks provider, builds tool list
internal/agent/loop.go    the loop + system prompt
internal/provider/        Provider interface, codex.go
internal/tools/           Tool struct, bash.go, edit.go
internal/auth/codex.go    ChatGPT device-code login + token refresh
```

## Loop

`agent.Run` calls `Provider.Step(task)`, runs the returned tool calls in parallel (one goroutine each, results kept in order), and calls `Step(results)` again. It stops when a step returns no calls.

## Plugins

- `tools.Tool` is a struct: name, description, JSON Schema params, `Run(ctx, input) (string, error)`. A new tool is one file plus one entry in the list in `main.go`.
- `provider.Provider` is one method: `Step(ctx, user, results) ([]Call, error)`. Each provider owns its history in its own wire format, so the loop stays format-agnostic. It streams text to stdout.

## Providers

**Codex** (`codex.go`): raw HTTP + SSE to `chatgpt.com/backend-api/codex/responses`.
- `store: false`, so the full `input` history is resent each turn. Items come from `response.output_item.done` and are replayed with `id` removed (stored ids 404).
- `include: reasoning.encrypted_content` keeps reasoning across turns without server storage.
- `prompt_cache_key` = per-run session id, so turns hit the same cache.
- Headers: `Authorization`, `ChatGPT-Account-ID` (from JWT), `originator: kh`, `session_id`.

## Auth (Codex)

Same flow as Hermes (`NousResearch/hermes-agent`), public Codex client id `app_EMoamEEZ73f0CkXaXp7hrann`:
1. `POST auth.openai.com/api/accounts/deviceauth/usercode` gives a user code.
2. User enters it at `auth.openai.com/codex/device`; we poll `/deviceauth/token` (403/404 = pending).
3. Exchange code + verifier at `/oauth/token`.

Tokens live in `~/.kh/codex.json` (0600), separate from `~/.codex` because refresh tokens are single-use. `auth.Token` takes an exclusive `flock`, refreshes 2 min before JWT `exp`, and re-reads inside the lock so concurrent runs never reuse a spent refresh token.

## Tools

**bash**: `/bin/bash --noprofile --norc -c` (starts in ~4 ms). Own process group, 2 min timeout kills the whole group, stdin is `/dev/null`. Output capped to first + last 10 KB. Failed commands return output + exit error as a normal result, so the model can react.
Safe list runs without asking: `rg grep cat head tail ls wc sed find pwd file tree` and `git status|diff|log|show`, piped together. Rejected as unsafe: `; & > < $ \`` or newline, `sed -i`, `find -exec|-delete|-ok`. Prompts are serialised with a mutex. `-y` skips them.

**edit**: `old` must appear exactly once, then replace. Empty `old` creates the file (`O_EXCL`, fails if it exists). Paths must resolve inside the project.

## Speed choices

- Short, fixed system prompt: fewer tokens and a stable cache prefix.
- Two tools only: smaller tool schema, and the model is already fluent in shell.
- Parallel tool execution; prompt tells the model to batch reads.
- Find-and-replace edits instead of whole-file rewrites (fewer output tokens).
- Output caps keep context small.
- Stdlib only. Single static binary.

## Not yet

Repo map (phase 3), `sandbox-exec` (phase 5). Until phase 5, `bash` can touch anything the user can, so the y/n prompt is the only guard.
