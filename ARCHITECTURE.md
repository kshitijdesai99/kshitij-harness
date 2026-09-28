# Architecture

```
cmd/kh/main.go            loads config, applies flags, builds tool list
internal/config/          defaults + ~/.kh/config.json
internal/repomap/         files + top-level symbols for the system prompt
internal/session/         save/load chats in ~/.kh/sessions
internal/agent/loop.go    the loop
internal/provider/        Provider interface, codex.go
internal/tools/           Tool struct, bash.go, edit.go, test/
internal/auth/codex.go    ChatGPT device-code login + token refresh
```

## Config

Built-in defaults, then `~/.kh/config.json` (only the fields it sets), then flags (`-model`, `-effort`, `-y`, `-nosandbox`; `-r` and `-s` pick a session). A missing file is fine.

| Key | Default |
|---|---|
| `model` | `gpt-6-luna` |
| `effort` | `medium` |
| `web_search` | `true` |
| `system` | short fixed prompt |
| `timeout_sec` | `30` |
| `output_cap` | `20000` |
| `map_cap` | `20000` (0 = off) |
| `safe` | `rg grep cat head tail ls wc sed find pwd file tree echo printf`, `git status/diff/log/show` |
| `yes` | `false` |
| `sandbox` | `true` |
| `writable` | `/tmp`, `/private/var/folders`, `~/Library/Caches`, `~/.cache`, `~/go` |

OpenAI URLs, the client id, headers and login timings are constants in code, not config, because changing them breaks the protocol.

## Loop

`agent.Run` calls `Provider.Step(task)`, runs the returned tool calls in parallel (one goroutine each, results kept in order), and calls `Step(results)` again. It stops when a step returns no calls.

## Chat and sessions

`kh "task"` runs one turn; `kh` alone (or `kh -i "task"`, which starts with that task) is a chat loop on the same provider, so history and the prompt cache carry over between messages. Each turn runs under `signal.NotifyContext`: Ctrl-C cancels the HTTP stream and kills running commands, prints `(stopped)`, then returns to the prompt. Ctrl-D quits.

After every turn the provider's history is saved to `~/.kh/sessions/<id>.json` (0600). Ids are `YYYYMMDD-HHMMSS.mmm`, so the latest is the last file. `-r` loads the latest, `-s id` a specific one; both work for chat or a one-off task. Resuming into chat first prints the old conversation in grey via `Provider.Replay` (`> ` user lines, `$ cmd`, `edit path`, `search: q`, replies). `kh sessions` lists the last 20 ids with their first message.

If a step fails mid-reply, Codex drops that reply's partial items, so a `function_call` without its output is never saved or resent. The chat prompt and the y/n question share one stdin reader (`tools.In`) so neither swallows the other's input.

## Plugins

- `tools.Tool` is a struct: name, description, JSON Schema params, `Run(ctx, input) (string, error)`. A new tool is one file plus one entry in the list in `main.go`.
- `provider.Provider` is `Step(ctx, user, results) ([]Call, error)` plus `Save`/`Load`/`Replay` of its history and `Stats` (tokens + TTFT for the turn). Each provider owns its history in its own wire format, so the loop stays format-agnostic. It streams text to stdout.

## Providers

**Codex** (`codex.go`): raw HTTP + SSE to `chatgpt.com/backend-api/codex/responses`.
- `store: false`, so the full `input` history is resent each turn. Items come from `response.output_item.done` and are replayed with `id` removed (stored ids 404).
- `include: reasoning.encrypted_content` keeps reasoning across turns without server storage.
- `reasoning.effort` from config.
- `web_search: true` adds `{"type": "web_search"}`, a server-side tool: OpenAI runs the search and returns a `web_search_call` item (printed as `search: <query>`), so there is nothing for kh to execute.
- `prompt_cache_key` = `kh_` + sha256(model, instructions, tools), so every session with the same prefix shares one warm cache (a random per-run key made each new chat start cold).
- Instructions = rules (from today's config) + repo map. Sessions save the map and reuse it on resume, since a rebuilt map would change the prompt and miss the cache for the whole history. Rules are not frozen: editing them costs one cache miss, then old sessions follow the new rules.
- Per turn, kh prints in grey: `(ttft 1.2s, total 8.4s, 12.4k in, 11.8k cached 95%, 310 out)`. TTFT is user message to first streamed text (`-` if none); tokens are summed from each step's `response.completed` usage.
- Headers: `Authorization`, `ChatGPT-Account-ID` (from JWT), `originator: kh`, `session_id`.

## Auth (Codex)

Same flow as Hermes (`NousResearch/hermes-agent`), public Codex client id `app_EMoamEEZ73f0CkXaXp7hrann`:
1. `POST auth.openai.com/api/accounts/deviceauth/usercode` gives a user code.
2. User enters it at `auth.openai.com/codex/device`; we poll `/deviceauth/token` (403/404 = pending).
3. Exchange code + verifier at `/oauth/token`.

Tokens live in `~/.kh/codex.json` (0600), separate from `~/.codex` because refresh tokens are single-use. `auth.Token` takes an exclusive `flock`, refreshes 2 min before JWT `exp`, and re-reads inside the lock so concurrent runs never reuse a spent refresh token.

## Tools

**bash**: `/bin/bash --noprofile --norc -c` (starts in ~4 ms). Own process group, `timeout_sec` kills the whole group, stdin is `/dev/null`. Output capped to `output_cap` (first + last half). Failed commands return output + exit error as a normal result, so the model can react. A timeout adds "timed out after Ns, try a narrower command"; a Ctrl-C does not.
Commands matching `safe` run without asking, joined by `| ; && ||`; an entry like `git diff` matches leading words. Always unsafe, whatever the config: `& > < $ \`` or newline, `sed -i`, `find -exec|-execdir|-delete|-ok`. One mutex covers printing `$ cmd` and the y/n question, so each question sits under its own command and parallel calls never print over it. `-y` skips them.

**Sandbox** (macOS, `sandbox: true`): every command runs under `sandbox-exec` with a profile that allows reads and network everywhere but writes only under the project, `writable` and `/dev`. Paths are resolved to real paths (`/tmp` is `/private/tmp`). Adds under 10 ms. A blocked write shows "Operation not permitted", the same error macOS privacy (TCC) gives for protected folders like Downloads, so kh appends a note naming both causes: `-nosandbox` / `writable` for writes, the terminal's Files & Folders permission for reads. Other OSes run without it.

**edit**: `old` must appear exactly once, then replace. Empty `old` creates the file (`O_EXCL`, fails if it exists). Paths must resolve inside the project.

## Repo map

Built once at start and appended to the system prompt, so it is part of the cached prefix. Files come from `git ls-files --cached --others --exclude-standard` (tracked + new, not ignored); if that is empty or fails, walk the dir skipping dot dirs, `node_modules`, `vendor`, and stop once the paths fill `map_cap` (so `~/Documents` maps in ~25 ms). `.go` files get their top-level types, vars, funcs and `Type.Method`s via `go/parser`; other files are listed by path. Stops at `map_cap` bytes, which also bounds the time spent (~65 ms for 20 KB of Go's own source tree).

## Speed choices

- Short, fixed system prompt: fewer tokens and a stable cache prefix.
- Two tools only: smaller tool schema, and the model is already fluent in shell.
- Parallel tool execution; prompt tells the model to batch reads.
- Find-and-replace edits instead of whole-file rewrites (fewer output tokens).
- Output caps keep context small.
- Repo map up front, so the model often opens the right file without searching.
- Stdlib only. Single static binary.
