# Project configuration

Use `.agents`, `.agent`, and `.claude` directories for startup instructions, on-demand capabilities and assets, local plugins, and external MCP tools and content.

Introduced **2026-10-30** · commit `752f184`.

## Discovery and precedence

- kh reads home configuration and ancestor directories from filesystem root through the startup working directory; this works outside Git repositories too.
- At each ancestor, plain `CLAUDE.md` then `AGENTS.md` entry points are loaded before dot-directory configuration. More specific locations override less specific ones. Within one location, the order is `.claude`, `.agent`, then `.agents`, so `.agents` wins matching definitions. The singular `.agent` directory remains supported.
- Startup discovery, settings, and the capability catalog are loaded at launch. **Restart kh to refresh startup configuration**; resuming a saved session does not reload an already-running process. Explicit capability loads reread the indexed file. For another existing target, use the directory-scoped tools below; these do not change startup settings, slash commands, or MCP connections.

## Assignment foundations

Task assignment resolves an inert project foundation: the applicable binding guidance and capability catalog, identified by an opaque source digest and schema version. A process-local cache holds at most 32 directory snapshots and returns independent copies of mutable configuration. Separate worker processes can compare foundation identifiers; they do **not** physically share cached configuration or provider history. No cache files are written, and server credentials are not persisted by this cache.

Every resolution validates filesystem dependencies, including content changes that preserve file size and timestamps, additions, deletions, and symlink changes. A validated hit avoids reparsing and rebuilding the catalog, not validation I/O. Changes invalidate the entry; configuration that keeps changing during discovery fails explicitly instead of supplying a stale snapshot. Supporting asset bodies remain lazy and are reread through ordinary capability loading.

The cache contains project context, not task closures, execution permissions, or conversation state. A fresh task still needs its own provider instructions and applicable guidance. A foundation identifier is not authorization to execute discovered scripts or start MCP connections. Assignment-boundary resolution does not turn ordinary running chats into live startup-configuration reloaders; the restart rule above still applies to their startup settings and connections.

## Target workspaces

Filesystem access is unrestricted by kh, subject to OS permissions. Before working in another repository or folder, inspect its applicable guidance and capabilities rather than applying the launch folder's rules globally.

Use `workspace` with `directory` set to the existing target folder:

| Action | Purpose |
|---|---|
| `inspect` | Read applicable binding guidance, aggregate capability counts, and the decision path without creating files or starting MCP/scripts. Use `detail: "decisions"` with optional `offset` and `limit` to request a bounded decision page. |
| `prepare` | Explicitly create or reuse `.agents` at the Git/workspace root before changes, never in every subdirectory. |
| `record` | Append a meaningful dated decision with `title`, `status`, `rationale`, `affected`, and `verification` (including outstanding work). |

Git targets use the nearest Git root. Outside Git, the root is the nearest ancestor with `.agents`, or the target itself; optional `root` can specify an existing containing workspace. Nested target guidance stays scoped to that target.

Default inspection omits per-file capability listings and historical decision prose. Search capabilities with `project_capability`; request decision text separately with `workspace` `inspect`, `detail: "decisions"`. Decision pages use byte offsets and an 8,192-byte default limit (maximum 16,384); continue using the returned `next_offset` rather than calculating offsets yourself.

Inspect the existing decision convention first. `decision_path` selects an existing root-relative Markdown convention (its parent must exist). The generic tool defaults to `.agents/decisions.md` when the argument is omitted; it does not persist a repository-specific selection. In this repository, always pass the owning feature page explicitly for inspection and recording, following [repository decisions](CONTRIBUTING.md#repository-decisions), rather than recreating that ledger. Records distinguish `proposed`, `implemented`, and `superseded`; identify a superseded decision in the rationale. Do not record secrets, transcripts, or routine logs. Inspection is read-only: preparation and recording are explicit writes, not side effects of lookup.

`project_capability` also accepts optional `directory` for `search`, `load`, and `read`. This loads that target's catalog on demand without changing the startup catalog or starting discovered MCP servers/scripts. Omit it to use the startup catalog.

## Instructions and on-demand capabilities

Binding `CLAUDE.md`, `AGENTS.md`, and unscoped rules provide startup context (`.md`, `.txt`, `.rst`, and `.adoc`). Skills, commands, supporting references, scripts, assets, and configuration-directory root prose such as `.agents/README.md` and decision records are indexed for on-demand inspection, not listed individually or copied into startup context. Startup provides aggregate source counts and a discovery notice. A rule with `paths` or `globs` frontmatter is diagnosed as unsupported and is not automatically applied.

The model can inspect the catalog through `project_capability`:

| Action | Input | Result |
|---|---|---|
| `search` | Optional `query` | Matching capability names, kinds, paths, and descriptions |
| `load` | Capability `name` | A command or skill body |
| `read` | Exact indexed absolute file `path` | Readable text from a reference, script, or other indexed file |

Search returns at most 50 metadata records with a truncation flag; the catalog holds at most 4,096 files. Loads recheck file paths and return bounded UTF-8 text. These operations expose reference data, not execution authority. Binary assets are indexed but not returned as text. Reading a script does not run it; execution still needs an ordinary tool call subject to command approval and OS permissions.

Slash invocation loads the selected command or skill body on demand. For example, `.agents/commands/review.md` defines `/review`, nested `commands/db/check.md` defines `/db:check`, and `skills/review/SKILL.md` defines `/review`. Invoke with arguments such as `/review current diff`. This is not a separate executable command runner or automatic agent spawn.

Single-line invocations expand `$ARGUMENTS`/`${ARGUMENTS}`, whitespace-separated `$1`, `$2`, and so on, and `${CLAUDE_SKILL_DIR}` in the command reference. This is not shell parsing or substitution; built-in kh slash controls take precedence over matching project names.

Startup shows one configuration summary rather than individual warnings. Use `/project diagnostics` for the full startup diagnostics without reloading configuration. MCP trust prompts and connection failures still appear immediately.

Text reads are limited to 512 KiB per file; automatic context has a 768 KiB safety budget, leaving room for core provider instructions. Binding `CLAUDE.md`, `AGENTS.md`, and rules are preserved rather than silently evicted to meet a compact-context target; exceeding either the 512 KiB per-file or 768 KiB aggregate binding budget produces an explicit error. Later, more-specific declarations retain their precedence. Symlinks are skipped, not followed. Skill/command descriptions are used for discovery, but model selection, allowed tools, hooks, and invocation restrictions in frontmatter are not enforced.

Trash directories, root metadata/log/lock/database files, saved chats, plans, environment snapshots, and caches are not project declarations and are skipped. Only the supported local plugin layout below is loaded. Project content is reference data, not permission to bypass kh's controls; database-backed [procedures](procedure-loading.md) remain separate.

## Compact context and tool discovery

A fresh session's default model-facing bootstrap aims for **roughly 5,000 tokens** by keeping capability catalogs and full tool contracts host-side and loading only task-relevant details. This is a baseline design target, not a universal hard cap on conversation history, a guarantee for arbitrary binding guidance, or a measured token total. Binding rules are not silently removed to hit it. User input, requested references, tool output, explicit memory settings, and accumulated history can grow context; [compaction](compaction.md) remains a separate operation.

Native and connected MCP tools are discoverable through two fixed model-facing tools:

| Tool | Input | Result or effect |
|---|---|---|
| `tool_registry` | `action: "search"`, keyword `query`, optional `limit` | Matching exact names and short summaries, not complete schemas. The default limit is 6; the maximum is 12. |
| `tool_registry` | `action: "describe"`, exact `name` | The registered tool's complete description, input schema, parameters, and required fields. |
| `tool_invoke` | Exact `name`, object-valued `arguments` | Execute the registered host implementation using the described contract. |

Search takes a nonempty keyword query of at most 256 bytes. Describe fails explicitly if the complete serialized contract JSON exceeds 32 KiB; it never returns a truncated schema. Search/describe execute no registered tools. Their output is untrusted reference data, not permission or instructions. Discover the needed operation, describe its contract, then invoke that exact name. This also applies to `workspace`, `project_capability`, memory, agents, visual tools, and generated MCP resource/prompt helpers; their host capabilities are retained rather than removed.

Invocation reaches the original implementation, including its existing approval wrappers and safety checks. It does not introduce an approval bypass, start newly declared MCP servers, or launch discovered scripts. Startup MCP trust and explicit browser authorization are unchanged. Call presentation identifies the underlying tool without printing nested argument values; approval policy still belongs to [command approvals](write-approvals.md).

[Hosted web search](web-search.md) is also activated on demand in the CLI when `web_search` permits it; ordinary fresh requests do not carry its hosted schema. Discovery does not restore eager tool schemas or capability catalogs. Hosted-call history has separate schema and compaction requirements, documented on the owning page.

`/project diagnostics` includes startup component byte counts, adapter-neutral tool-metadata JSON size, and a bytes/4 estimate of the instruction/map floor. It warns when that floor exceeds the target, without dropping guidance. The floor excludes tool metadata, history, user input, recalled memory, provider framing, and hosted tools; the tool JSON is not the provider's wire schema. These diagnostics are not tokenizer output, provider usage, or proof that all future turns fit the target. [Turn metrics](turn-metrics.md) report provider-supplied usage for actual requests. Automatic memory uses a smaller [default budget](keyword-recall.md); explicit saved settings are retained.

## Local plugins

A local bundle lives under a configuration directory's `plugins/<bundle>/` and needs `.claude-plugin/plugin.json` with a `name`. Standard `skills/`, `commands/`, `rules/`, `scripts/`, `references/`, and `assets/` content is indexed lazily; plugin rules are references, not automatically applied startup rules.

The supported manifest subset accepts `skills`, `commands`, and `rules` as a `./relative` path or an array of paths. Custom command paths replace the standard `commands/` scan; custom skill paths supplement `skills/`. The local `rules` extension indexes lazy references alongside `rules/`. `mcpServers` can be an inline server map or a `./relative` JSON file, loaded after the bundle's `.mcp.json`, with matching server names replaced by the later declaration. Paths must stay inside the local bundle, without symlinks or parent traversal. Slash expansion and MCP declarations substitute `${CLAUDE_PLUGIN_ROOT}` with the bundle directory. A `project_capability` load reports the skill directory and plugin root alongside the source text.

Plugin commands and skills are namespaced: a bundle named `demo` exposes `/demo:review`, and `commands/db/check.md` becomes `/demo:db:check`. MCP server names are prefixed with the plugin name, such as `demo:api`; connecting them still requires the MCP trust policy below.

Direct bundles load unless disabled by `enabledPlugins` in that configuration directory's settings, using a plugin name or `name@marketplace` key; local settings override settings. Version-2 `plugins/installed_plugins.json` records require an explicitly enabled qualified key and an absolute `installPath` under that same configuration directory's `plugins/` tree. User-scoped records are accepted; project/local records must match the declaring project directory.

This reads existing local bundles only: kh does not resolve marketplaces, download, install, or update plugins. Unsupported manifest fields do not gain runtime behavior. The explicit executable exception is the bounded [plugin hook subset](#plugin-hooks) below; loading a command, skill, script, or other Markdown reference remains inert.

## Plugin hooks

Plugin hooks are a generic, bounded compatibility subset, not a full Claude Code runtime. Ordinary inert plugin references can load by default; executable hook discovery requires explicit `enabledPlugins` enablement. Neither discovery nor enablement authorizes execution. Hook execution needs separate explicit plugin trust. Trust applies to executable plugin code, not to arbitrary instructions found in its Markdown or hook output.

Hooks default to `hooks/hooks.json`. The manifest's `hooks` field can instead contain an inline hook map, a `./relative.json` path, or an array of such paths confined to the bundle. Files can wrap event maps in `{"hooks": {...}}`; each event contains matcher groups with a `hooks` array of `type: "command"` declarations. A `command` without `args` is explicitly a `/bin/sh -c` command; `command` with `args`, or the alternative nonempty `argv` array, is direct execution. Optional `env`, `timeout` in seconds, and `requiresNode` are supported. Unknown command fields, unsupported events and unsafe paths are diagnosed and skipped. This executable JSON subset does not turn Markdown into shell code.

List current plugin identities and grant or revoke a specific identity explicitly:

```bash
kh plugins list
kh plugins trust TRUST_ID
kh plugins revoke TRUST_ID
```

The grant is stored in `~/.kh/plugin-trust.json`, separately from `enabledPlugins`. Use the full fingerprint shown by `list`; it covers the plugin root, plugin identity, and normalized hook definitions. Changing those definitions invalidates the old grant. `revoke` also accepts an old identity. There is no environment or auto-mode override for hook trust. Review and trust the new identity rather than assuming prior enablement or auto approval covers it. Restart the chat to use changed startup declarations or trust grants.

**Review a plugin before trusting it.** Hook commands run with the current user's permissions and can change project files, install or alter Git hooks, launch detached processes, invoke an external recorder, or sync data to another service. Hook timeouts bound how long kh waits; they are not containment and cannot undo file changes or reliably stop detached descendants. Trusting hooks is distinct from MCP trust and ordinary tool-call approval.

### Runtime bounds

Hooks receive JSON on stdin, including `hook_event_name`, `session_id`, `cwd`, `transcript_path` and `stop_hook_active`; event-specific fields include `prompt`, `source`, `tool_name` and `tool_input`. The compatibility environment supplies `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PROJECT_DIR`, `CLAUDE_SESSION_ID` and `CLAUDE_CODE_SESSION_ID`. The caller's `CLAUDE_CONFIG_DIR` remains unchanged (including when unset), so external Claude recorders keep their normal configuration and authentication. Approved plugin-related shell work uses the same session environment; this is not permission to run a command without normal approval.

Each hook defaults to **five seconds** and accepts a positive timeout of at most **30 seconds**; longer declarations are diagnosed and skipped. Stdout and stderr each have a 64 KiB limit. Exceeding output limits cancels the command; invalid JSON, nonzero exits and timeouts become diagnostics rather than permission changes or automatic retries. Hook output is untrusted context, never a replacement system prompt or execution authority.

### Lifecycle and transcript boundary

Hooks run only in user-facing root chats with a matching trust grant, not delegated workers. The five supported events are:

| Event | Boundary |
|---|---|
| `SessionStart` | Once at startup, with `source` distinguishing `startup` and `resume`. |
| `UserPromptSubmit` | Each nonempty actual user message, including initial, steering and queued input, before provider execution. |
| `PreToolUse` | Supported native edits after preflight and required approval, before mutation; Claude-compatible `Edit`, `Write` and `MultiEdit` labels support matchers. |
| `Stop` | After a completed assistant response; may request one bounded continuation, not an unbounded self-prompt loop. |
| `SessionEnd` | Best-effort shutdown with a separate five-second budget; abrupt termination cannot guarantee delivery. |

The continuation carries `stop_hook_active` so it cannot request another continuation, and its model/tool work has a two-minute deadline. Actual steering or queued user input ends that continuation boundary and restores the caller's cancellation context. Unsupported Claude events or response features do not acquire runtime behavior.

Hooks consume a separate private, Claude-compatible conversation projection, not the resumable provider history or the richer [conversation archive](saved-sessions.md#conversation-archive). It lives under `<claude-config>/projects/kh-plugin-<cwd-sha256>/<session-id>.jsonl`, using the caller's `CLAUDE_CONFIG_DIR` or `~/.claude` by default. The separate kh namespace allows Cogit's session-ID scanner to find the projection without redirecting a spawned Claude recorder away from its authenticated configuration or overwriting native Claude transcripts. The kh namespace is `0700` and its files `0600`; existing shared configuration/project directory permissions are not changed. These files are not encrypted. Treat the text as private before copying or syncing it.

When reopening a session, kh copies a validated legacy projection from `~/.kh/plugin-transcripts/config/projects/<escaped-cwd>/<session-id>.jsonl` into an empty new projection, preserving record IDs and timestamps. It leaves the original intact and never merges over a nonempty published projection. No credentials are copied.

Its conversation content is actual user input and completed assistant replies. It excludes delegated task prompts and peer messages; workers do not turn their assignments into user-hook events. Compaction does not rewrite this projection, and plugin edits to it do not rewrite provider history. Stable receipt IDs avoid duplicate appends; messages are limited to 1 MiB and the projection to 64 MiB. This is not a backfill of old provider history or a replacement recovery format.

### Cogit

Cogit is one consumer of this generic subset, not a built-in recorder or a claim of complete Claude plugin compatibility. It requires **Node.js 22.13 or newer** in the hook process's environment. Review its executable hooks and external recording/sync behavior before granting trust. Cogit's session-inspection and edit instructions use kh's explicit adaptation rather than assuming Claude's provider transcript format or tools:

- A `Stop` request for `/cogit:record` loads the recording skill as reference context for the one bounded continuation; it does not execute the slash text as a shell command.
- For Cogit's `session show` operation, use an explicit Bash tool call with the plugin CLI and arguments from the loaded skill. It receives the isolated session environment and remains subject to normal command approval. This is not a new `kh session show` command.
- Translate Claude-style file-writing instructions to native `edit`: a new file uses `{"path":"...","old":"","new":"..."}`; an existing file needs its exact old text. Native edits complete their usual preflight and approval before `PreToolUse` runs. Creates map to `Write`, single replacements to `Edit`, and batches to `MultiEdit` per affected path.

Markdown commands and skills remain references: kh does not add shell expansion or an executable Markdown runner. In particular, `!`-backtick snippets in a skill stay inert; use an explicit approved tool call instead.

## MCP and safety

Declare `mcpServers` in `mcp.json`, `.mcp.json`, `settings.json`, or `settings.local.json` within any supported configuration directory, or in an ancestor/home `.mcp.json`. At each location, the standalone `.mcp.json` loads first; within `.claude`, `.agent`, or `.agents`, the order is `mcp.json`, `.mcp.json`, settings, then local settings. Matching server names replace earlier definitions. Server definitions accept `command`, `args`, and `env` for stdio, or `url` and `headers` for HTTP, plus `type` to select transport. JSON values and environment variables are configuration, not prompt instructions.

MCP servers expose real tools and advertised resources/prompts over stdio or Streamable HTTP. Connecting requires startup trust; individual calls, including content reads, require tool approval when [auto approval](write-approvals.md) is off. Auto mode can approve both steps automatically.

With auto enabled, startup replaces per-server trust action lines and the loaded-tool notice with one concise status summary, such as `(MCP: 2 connected, 1 failed)`. It counts server startup results, not tools: resource-only and zero-tool servers can be connected. Failed and not-connected counts appear when nonzero; no summary is printed when no servers are configured. This is a startup snapshot, not a live-health or product-authentication check; use `/connector` for details. Connection errors and OAuth diagnostics remain visible. With auto off, each server still asks for startup trust; per-call action logging/approval and explicit OAuth browser consent are unchanged.

Resource and prompt content is lazy: startup does not fetch their lists or bodies. For a server named `docs`, kh registers these operations only when the server advertises the relevant capability; discover and describe them through `tool_registry`, then call them through `tool_invoke`:

| Tool | Input | Purpose |
|---|---|---|
| `mcp__docs__resources_list` | `{}` | List available resource URIs |
| `mcp__docs__resources_templates_list` | `{}` | List URI templates |
| `mcp__docs__resources_read` | `uri` | Read a resource at a concrete URI |
| `mcp__docs__prompts_list` | `{}` | List prompts and their arguments |
| `mcp__docs__prompts_get` | `name`, optional string-valued `arguments` | Get prompt messages |

Templates describe URI patterns; use a concrete URI for reading. Returned prompt messages are tool-result data, not a replacement system prompt or permission to execute instructions. Resource bodies and prompt messages are serialized JSON, including nontext content rather than native image attachments.

**MCP tools have real external effects.** A server may run local processes, change files, use credentials, or contact remote services under its own permissions. kh does not confine server or Bash filesystem access. Review server definitions and tool behavior before trusting them, especially with auto enabled.

Home JSON configuration and environment values used for server credentials are not copied into model context. Do not put secrets in Markdown instructions or references: binding instructions enter context at startup and indexed text can be loaded on demand. This does not redact secrets returned by an external tool.

### Session connector inventory

Use `/connector` to list this session's MCP startup snapshot without a model request, live probe, reconnection, or authorization. Connected servers appear first, followed by failed and not-connected servers, each group sorted by name. Counts include advertised tools only, not generated resource/prompt helpers; resource-only and zero-tool servers can still be connected. Unsafe server labels are redacted and failure details stay generic.

Exposed service hints come conservatively from explicit tokens in discovered tool names, including camel-case operation names. Hints such as Jira, Confluence, and Slack are best-effort labels. A gateway's generic discovery or execution tools cannot establish which products it connects to, so hints may be incomplete. Registered availability is not live health, verified product authentication, or proof of permissions. Restart to refresh startup discovery; declarations added later are not part of this snapshot.

### Native HTTP OAuth

A trusted Streamable HTTP server can use native OAuth without a provider-specific bridge or credentials in its declaration. Configure its `url` as usual. When the server returns HTTP 401, kh discovers Protected Resource Metadata (PRM) from the Bearer challenge's `resource_metadata` URL, or the endpoint-path/root well-known locations when that parameter is absent. It then discovers the first advertised authorization server through OAuth authorization-server metadata, with OpenID Connect metadata fallbacks. The issuer must match exactly; the protected resource must match the MCP endpoint without its query string.

Initial login uses dynamic client registration (DCR) for a native public client, authorization code flow with S256 PKCE and random state, and a temporary `127.0.0.1` callback. **Browser login is available only in an interactive root chat**, with terminal stdin and stdout, not `-p`, redirected sessions, or delegated agents. kh asks for explicit consent before opening the browser, even with auto approval enabled; the prompt shows only the authorization origin, not private URL parameters. macOS uses `open`; Linux uses `xdg-open`. The browser step has a separate five-minute deadline and can be interrupted with Ctrl-C; normal startup attempts retain their 15-second deadline.

Headless sessions and agents can reuse cached access tokens and refresh grants without opening a browser. Known token expiry triggers a refresh before sending the token; a 401 can also trigger a serialized refresh. Recovery is bounded: a cold connection can replay once with a discovered cached token and once more after refreshing a rejected token; requests with an already-loaded token allow one recovery replay. Ambiguous network failures are not replayed for authorization. If a fresh login is needed, reconnect from an interactive root terminal. Ordinary tool calls never launch a browser. Server trust and per-call approval still apply independently of login.

Explicit HTTP `Authorization` headers, matched case-insensitively, take precedence and disable native OAuth for that server. Other configured headers remain on MCP requests; they are not forwarded to metadata, registration, or token endpoints.

Credentials are stored outside project configuration at `~/.kh/mcp/oauth`, keyed by MCP endpoint, issuer, and resource. The cache is **not encrypted**: it relies on current-user ownership, a `0700` directory and `0600` credential/lock files. Unsafe ownership, permissions, symlinks at the cache directory/files, and hard-linked files are rejected. Atomic writes and cross-process locks protect rotating refresh tokens across sessions. Treat the cache as secret material; do not copy it into project instructions or share it. OAuth diagnostics omit tokens, codes, and authorization URL parameters; external tool results are still not secret-redacted.

Native OAuth requires an authorization server advertising S256 PKCE and supporting public-client DCR. There is no configuration for pre-registered client IDs/secrets, confidential clients, or device-code login. Discovery/token requests require public HTTPS destinations, except literal-loopback development servers; redirects and ambient proxies are not used for those requests. Private-network authorization services are not supported by this flow. Existing static-header authentication and stdio bridges remain alternatives when these requirements do not fit.

## Compatibility limits

MCP does not support sampling or legacy SSE transport. HTTP supports POST response streams, not GET streams or resumption. Startup discovery times out after 15 seconds per attempt, excluding the separate OAuth browser step above; calls after 60 seconds. OAuth metadata/token requests have a 10-second HTTP timeout and 1 MiB JSON response limit. List operations follow pagination with bounded page, entry, and response sizes; incomplete lists fail rather than silently dropping entries. Large content output is bounded with a truncation marker.

This is a supported subset of filesystem configuration and local plugin content, not a complete Claude Code runtime. Hooks outside the [supported plugin subset](#plugin-hooks), permissions, and other unsupported settings produce diagnostics; they are not silently executed or treated as kh authorization. Assets are indexed rather than launched. Restart after editing startup settings or declarations used by startup; directory-scoped inspection can read another target without restarting.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-30 | Added home/ancestor configuration, lazy skills/commands/assets, local plugin references, and approved MCP tools/resources/prompts. | `752f184` |
| 2026-10-31 | Added plain ancestor entry points, explicit target workspace inspection/preparation/decisions, and directory-scoped capability lookup. | Current change |
| 2026-11-01 | Added generic native HTTP OAuth discovery, interactive root browser login, and locked cached credentials/refresh for headless sessions and agents. | Current change |
| 2026-11-01 | Added `/connector` session startup inventory with advertised-tool counts, resource/prompt availability, and conservative service hints without live probes. | Current change |
| 2026-11-01 | Replaced auto-mode per-server startup trust chatter and tool-count notice with one server-status summary; manual trust, OAuth consent, errors and per-call policy remain unchanged. | Current change |
| 2026-11-02 | Kept catalogs, root references and decisions on demand; added registry search/describe/invoke, compact workspace inspection, and baseline context diagnostics while preserving binding guidance and original tool policy. | Current change |
| 2026-11-03 | Made hosted web activation discoverable without eagerly adding its schema to fresh CLI requests. | Current change |
| 2026-11-05 | Added bounded process-local assignment foundations with source-content validation and independent configuration snapshots. | Current change |
| 2026-11-07 | Added explicitly enabled and separately trusted plugin command hooks, private root-user/completed-assistant projection, and a bounded Cogit recording adaptation without executable Markdown. | Working tree (uncommitted) |
| 2026-11-07 | Preserved external Claude authentication by publishing projections in a kh-only namespace under the actual Claude config; retained legacy projection history on reopen. | Working tree (uncommitted) |

## Inspect

- Compact tools: [host registry and facade](../internal/tools/registry.go), [call presentation](../internal/tools/describe.go), [registry/approval/result coverage](../internal/tools/test/registry_test.go).
- Bootstrap diagnostics: [component accounting](../cmd/kh/bootstrap.go).
- Configuration: [discovery and context](../internal/projectconfig/projectconfig.go), [slash expansion](../internal/projectconfig/commands.go), [on-demand loading](../internal/projectconfig/tools.go), [local plugins](../internal/projectconfig/plugins.go).
- Plugin hooks: [inert discovery](../internal/projectconfig/hooks.go), [fingerprinted trust grants](../internal/projectconfig/hooktrust.go), [discovery and trust tests](../internal/projectconfig/test/hooks_test.go), [bounded runner](../internal/pluginhooks/runner.go), [private projection](../internal/pluginhooks/transcript.go), [CLI lifecycle and trust commands](../cmd/kh/plugin_hooks.go).
- Assignment foundations: [cache and source validation](../internal/projectconfig/foundation.go), [invalidation/isolation coverage](../internal/projectconfig/test/foundation_test.go).
- Target workspaces: [inspect/prepare/record](../internal/projectconfig/workspace.go), [workspace tests](../internal/projectconfig/test/workspace_test.go), [compact context and decision paging coverage](../internal/projectconfig/test/compact_context_test.go).
- MCP: [client](../internal/mcp/mcp.go), [resource and prompt operations](../internal/mcp/content.go), [CLI trust and approval](../cmd/kh/project.go).
- Connector inventory: [formatter](../cmd/kh/connector.go), [CLI coverage](../cmd/kh/test/connector_test.go), [startup observation](../internal/mcp/test/connector_test.go), [native command reservation](../internal/projectconfig/test/connector_test.go).
- Native OAuth: [discovery and authorization](../internal/mcp/oauth.go), [credential cache](../internal/mcp/oauth_store.go), [CLI browser policy](../cmd/kh/mcp_oauth.go).
- Representative coverage: [configuration](../internal/projectconfig/test/projectconfig_test.go), [lazy loading](../internal/projectconfig/test/lazy_test.go), [plugins](../internal/projectconfig/test/plugins_test.go), [MCP content](../internal/mcp/test/content_test.go), [CLI integration](../cmd/kh/test/project_config_test.go), [OAuth protocol](../internal/mcp/test/oauth_test.go), [OAuth CLI](../cmd/kh/test/mcp_oauth_test.go).

[Feature index](README.md)


## 2026-11-07 — Bounded plugin hooks with explicit trust and isolated conversation projection

The original isolated `CLAUDE_CONFIG_DIR` path/environment decision is superseded by the authentication-preserving projection decision below; the hook trust and conversation-content boundaries remain in force.

- Status: implemented
- Rationale: Support generic local plugin command hooks and Cogit recording without claiming full Claude parity or executing Markdown. Explicit enabledPlugins discovery and fingerprinted execution trust remain separate; hook timeouts are latency bounds, not containment. Supersedes the former no-plugin-hook-runtime limitation above.
- Affected: Plugin discovery/trust, bounded hook runner, root-chat lifecycle, approved edit/tool adaptation and private projection; current usage in this page, saved-session cross-reference, feature index and release-notes/2026-11-07.md.
- Verification / outstanding: 2026-11-07: Compared documented CLI, five events, trust identity, isolated transcript path/environment, and five-second default/30-second maximum against current source and owning-worker contracts. Checked 118 local links/anchors across four changed documentation files and scoped git diff --check successfully. Documentation worker did not run application tests; runtime/integration suites and end-to-end Cogit verification remain outstanding.


## 2026-11-07 — Preserve Claude runtime authentication while publishing kh-only projections

- Status: implemented
- Rationale: Supersedes only the isolated CLAUDE_CONFIG_DIR projection path/environment in the earlier 2026-11-07 bounded-plugin-hooks decision. Actual Cogit side recording inherited that private unauthenticated config. Keep the caller's runtime configuration unchanged and publish transcripts in a kh-plugin-<cwd-sha256> project namespace under the actual Claude config, without copying credentials or changing recorder mode/cadence.
- Affected: internal/pluginhooks runner and transcript storage/migration; projection and CLI lifecycle tests; owning guide and release-notes/2026-11-07.md.
- Verification / outstanding: go test -race ./internal/pluginhooks/test ./internal/tools/test passed; go test ./cmd/kh/test -run '^TestPluginLifecycleCLI$' -count=1 passed; gofmt and git diff --check passed. Coverage includes inherited default/custom Claude config, child-process authentication sentinel and transcript scan, legacy byte/identity preservation and idempotence, shared-directory permissions, symlink replacement, hard links and cwd namespace collisions. Independent review, broader tests, and approved real Cogit side-recording verification remain outstanding; no installed process or user config/state was changed.
