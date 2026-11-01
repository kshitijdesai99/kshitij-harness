# Project configuration

Use `.agents`, `.agent`, and `.claude` directories for startup instructions, on-demand capabilities and assets, local plugins, and external MCP tools and content.

Introduced **2026-10-30** · commit `752f184`.

## Discovery and precedence

- kh reads home configuration and ancestor directories from filesystem root through the startup working directory; this works outside Git repositories too.
- At each ancestor, plain `CLAUDE.md` then `AGENTS.md` entry points are loaded before dot-directory configuration. More specific locations override less specific ones. Within one location, the order is `.claude`, `.agent`, then `.agents`, so `.agents` wins matching definitions. The singular `.agent` directory remains supported.
- Startup discovery, settings, and the capability catalog are loaded at launch. **Restart kh to refresh startup configuration**; resuming a saved session does not reload an already-running process. Explicit capability loads reread the indexed file. For another existing target, use the directory-scoped tools below; these do not change startup settings, slash commands, or MCP connections.

## Target workspaces

Filesystem access is unrestricted by kh, subject to OS permissions. Before working in another repository or folder, inspect its applicable guidance and capabilities rather than applying the launch folder's rules globally.

Use `workspace` with `directory` set to the existing target folder:

| Action | Purpose |
|---|---|
| `inspect` | Read applicable guidance, capability metadata, and bounded decisions without creating files or starting MCP/scripts. |
| `prepare` | Explicitly create or reuse `.agents` at the Git/workspace root before changes, never in every subdirectory. |
| `record` | Append a meaningful dated decision with `title`, `status`, `rationale`, `affected`, and `verification` (including outstanding work). |

Git targets use the nearest Git root. Outside Git, the root is the nearest ancestor with `.agents`, or the target itself; optional `root` can specify an existing containing workspace. Nested target guidance stays scoped to that target.

Inspect the existing decision convention first. `decision_path` selects an existing root-relative Markdown convention (its parent must exist), defaulting to `.agents/decisions.md`. Records distinguish `proposed`, `implemented`, and `superseded`; identify a superseded decision in the rationale. Do not record secrets, transcripts, or routine logs. Inspection is read-only: preparation and recording are explicit writes, not side effects of lookup.

`project_capability` also accepts optional `directory` for `search`, `load`, and `read`. This loads that target's catalog on demand without changing the startup catalog or starting discovered MCP servers/scripts. Omit it to use the startup catalog.

## Instructions and on-demand capabilities

Core instruction files, root prose, and unscoped rules provide startup context (`.md`, `.txt`, `.rst`, and `.adoc`). Commands and skills contribute a compact catalog instead of their full bodies; supporting references, scripts, and assets are indexed for later inspection. A rule with `paths` or `globs` frontmatter is diagnosed as unsupported and is not automatically applied.

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

Text reads are limited to 512 KiB per file; automatic context has a 768 KiB budget, leaving room for core provider instructions. Core `CLAUDE.md`, `AGENTS.md` and rules take priority over capability metadata and asset listings. Within each priority, later, more-specific declarations win. Symlinks are skipped, not followed. Skill/command descriptions are used for discovery, but model selection, allowed tools, hooks, and invocation restrictions in frontmatter are not enforced.

Trash directories, root metadata/log/lock/database files, saved chats, plans, environment snapshots, and caches are not project declarations and are skipped. Only the supported local plugin layout below is loaded. Project content is reference data, not permission to bypass kh's controls; database-backed [procedures](procedure-loading.md) remain separate.

## Local plugins

A local bundle lives under a configuration directory's `plugins/<bundle>/` and needs `.claude-plugin/plugin.json` with a `name`. Standard `skills/`, `commands/`, `rules/`, `scripts/`, `references/`, and `assets/` content is indexed lazily; plugin rules are references, not automatically applied startup rules.

The supported manifest subset accepts `skills`, `commands`, and `rules` as a `./relative` path or an array of paths. Custom command paths replace the standard `commands/` scan; custom skill paths supplement `skills/`. The local `rules` extension indexes lazy references alongside `rules/`. `mcpServers` can be an inline server map or a `./relative` JSON file, loaded after the bundle's `.mcp.json`, with matching server names replaced by the later declaration. Paths must stay inside the local bundle, without symlinks or parent traversal. Slash expansion and MCP declarations substitute `${CLAUDE_PLUGIN_ROOT}` with the bundle directory. A `project_capability` load reports the skill directory and plugin root alongside the source text.

Plugin commands and skills are namespaced: a bundle named `demo` exposes `/demo:review`, and `commands/db/check.md` becomes `/demo:db:check`. MCP server names are prefixed with the plugin name, such as `demo:api`; connecting them still requires the MCP trust policy below.

Direct bundles load unless disabled by `enabledPlugins` in that configuration directory's settings, using a plugin name or `name@marketplace` key; local settings override settings. Version-2 `plugins/installed_plugins.json` records require an explicitly enabled qualified key and an absolute `installPath` under that same configuration directory's `plugins/` tree. User-scoped records are accepted; project/local records must match the declaring project directory.

This reads existing local bundles only: kh does not resolve marketplaces, download, install, or update plugins. Hooks are not loaded or executed, and unsupported manifest fields do not gain runtime behavior.

## MCP and safety

Declare `mcpServers` in `mcp.json`, `.mcp.json`, `settings.json`, or `settings.local.json` within any supported configuration directory, or in an ancestor/home `.mcp.json`. At each location, the standalone `.mcp.json` loads first; within `.claude`, `.agent`, or `.agents`, the order is `mcp.json`, `.mcp.json`, settings, then local settings. Matching server names replace earlier definitions. Server definitions accept `command`, `args`, and `env` for stdio, or `url` and `headers` for HTTP, plus `type` to select transport. JSON values and environment variables are configuration, not prompt instructions.

MCP servers expose real tools and advertised resources/prompts over stdio or Streamable HTTP. Connecting requires startup trust; individual calls, including content reads, require tool approval when [auto approval](write-approvals.md) is off. Auto mode can approve both steps automatically.

With auto enabled, startup replaces per-server trust action lines and the loaded-tool notice with one concise status summary, such as `(MCP: 2 connected, 1 failed)`. It counts server startup results, not tools: resource-only and zero-tool servers can be connected. Failed and not-connected counts appear when nonzero; no summary is printed when no servers are configured. This is a startup snapshot, not a live-health or product-authentication check; use `/connector` for details. Connection errors and OAuth diagnostics remain visible. With auto off, each server still asks for startup trust; per-call action logging/approval and explicit OAuth browser consent are unchanged.

Resource and prompt content is lazy: startup does not fetch their lists or bodies. For a server named `docs`, kh exposes these operations only when the server advertises the relevant capability:

| Tool | Input | Purpose |
|---|---|---|
| `mcp__docs__resources_list` | `{}` | List available resource URIs |
| `mcp__docs__resources_templates_list` | `{}` | List URI templates |
| `mcp__docs__resources_read` | `uri` | Read a resource at a concrete URI |
| `mcp__docs__prompts_list` | `{}` | List prompts and their arguments |
| `mcp__docs__prompts_get` | `name`, optional string-valued `arguments` | Get prompt messages |

Templates describe URI patterns; use a concrete URI for reading. Returned prompt messages are tool-result data, not a replacement system prompt or permission to execute instructions. Resource bodies and prompt messages are serialized JSON, including nontext content rather than native image attachments.

**MCP tools have real external effects.** A server may run local processes, change files, use credentials, or contact remote services under its own permissions. kh does not confine server or Bash filesystem access. Review server definitions and tool behavior before trusting them, especially with auto enabled.

Home JSON configuration and environment values used for server credentials are not copied into model context. Do not put secrets in Markdown instructions: supported text is intentionally included in context. This does not redact secrets returned by an external tool.

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

This is a supported subset of filesystem configuration and local plugin content, not a complete Claude Code runtime. Unsupported hooks, permissions, and other unsupported settings produce diagnostics; they are not silently executed or treated as kh authorization. Assets are indexed rather than launched. Restart after editing startup settings or declarations used by startup; directory-scoped inspection can read another target without restarting.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-30 | Added home/ancestor configuration, lazy skills/commands/assets, local plugin references, and approved MCP tools/resources/prompts. | `752f184` |
| 2026-10-31 | Added plain ancestor entry points, explicit target workspace inspection/preparation/decisions, and directory-scoped capability lookup. | Current change |
| 2026-11-01 | Added generic native HTTP OAuth discovery, interactive root browser login, and locked cached credentials/refresh for headless sessions and agents. | Current change |
| 2026-11-01 | Added `/connector` session startup inventory with advertised-tool counts, resource/prompt availability, and conservative service hints without live probes. | Current change |
| 2026-11-01 | Replaced auto-mode per-server startup trust chatter and tool-count notice with one server-status summary; manual trust, OAuth consent, errors and per-call policy remain unchanged. | Current change |

## Inspect

- Configuration: [discovery and context](../internal/projectconfig/projectconfig.go), [slash expansion](../internal/projectconfig/commands.go), [on-demand loading](../internal/projectconfig/tools.go), [local plugins](../internal/projectconfig/plugins.go).
- Target workspaces: [inspect/prepare/record](../internal/projectconfig/workspace.go), [workspace tests](../internal/projectconfig/test/workspace_test.go).
- MCP: [client](../internal/mcp/mcp.go), [resource and prompt operations](../internal/mcp/content.go), [CLI trust and approval](../cmd/kh/project.go).
- Connector inventory: [formatter](../cmd/kh/connector.go), [CLI coverage](../cmd/kh/test/connector_test.go), [startup observation](../internal/mcp/test/connector_test.go), [native command reservation](../internal/projectconfig/test/connector_test.go).
- Native OAuth: [discovery and authorization](../internal/mcp/oauth.go), [credential cache](../internal/mcp/oauth_store.go), [CLI browser policy](../cmd/kh/mcp_oauth.go).
- Representative coverage: [configuration](../internal/projectconfig/test/projectconfig_test.go), [lazy loading](../internal/projectconfig/test/lazy_test.go), [plugins](../internal/projectconfig/test/plugins_test.go), [MCP content](../internal/mcp/test/content_test.go), [CLI integration](../cmd/kh/test/project_config_test.go), [OAuth protocol](../internal/mcp/test/oauth_test.go), [OAuth CLI](../cmd/kh/test/mcp_oauth_test.go).

[Feature index](README.md)
