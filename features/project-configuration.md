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

## Compatibility limits

MCP does not support sampling, OAuth login, or legacy SSE transport. HTTP supports POST response streams, not GET streams or resumption; use static headers for authentication. Startup discovery times out after 15 seconds per server; calls after 60 seconds. List operations follow pagination with bounded page, entry, and response sizes; incomplete lists fail rather than silently dropping entries. Large content output is bounded with a truncation marker.

This is a supported subset of filesystem configuration and local plugin content, not a complete Claude Code runtime. Unsupported hooks, permissions, and other unsupported settings produce diagnostics; they are not silently executed or treated as kh authorization. Assets are indexed rather than launched. Restart after editing startup settings or declarations used by startup; directory-scoped inspection can read another target without restarting.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-30 | Added home/ancestor configuration, lazy skills/commands/assets, local plugin references, and approved MCP tools/resources/prompts. | `752f184` |
| 2026-10-31 | Added plain ancestor entry points, explicit target workspace inspection/preparation/decisions, and directory-scoped capability lookup. | Current change |

## Inspect

- Configuration: [discovery and context](../internal/projectconfig/projectconfig.go), [slash expansion](../internal/projectconfig/commands.go), [on-demand loading](../internal/projectconfig/tools.go), [local plugins](../internal/projectconfig/plugins.go).
- Target workspaces: [inspect/prepare/record](../internal/projectconfig/workspace.go), [workspace tests](../internal/projectconfig/test/workspace_test.go).
- MCP: [client](../internal/mcp/mcp.go), [resource and prompt operations](../internal/mcp/content.go), [CLI trust and approval](../cmd/kh/project.go).
- Representative coverage: [configuration](../internal/projectconfig/test/projectconfig_test.go), [lazy loading](../internal/projectconfig/test/lazy_test.go), [plugins](../internal/projectconfig/test/plugins_test.go), [MCP content](../internal/mcp/test/content_test.go), [CLI integration](../cmd/kh/test/project_config_test.go).

[Feature index](README.md)
