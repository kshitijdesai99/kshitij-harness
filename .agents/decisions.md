# Repository decisions

This file records repository-specific engineering decisions, not executable instructions or a transcript. Existing repository guidance and the user's current request still apply. Cross-repository decisions belong in the target workspace's own record.

## 2026-10-31 — Remove filesystem access modes and folder grants

- Status: implemented.
- Rationale: the user approved using any kh session as a control point across folders and repositories. This supersedes the earlier optional folder-sandbox proposal and the former filesystem grant model.
- Affected: shell execution, structured edits, notebook startup, command approval state, CLI flags/configuration, agents and documentation. Remove `--nosandbox`, `--auto-write`, `write_dirs`, `sandbox`, `writable`, and folder-approval APIs; retain live `/auto` command approval and OS permissions. Retain edit staging, alias handling, pinned descriptors, cancellation and output limits.
- Verification / outstanding: final `go test ./...` and `go vet ./...` passed; race tests for tools, project configuration, terminal and visual packages passed. Targeted notebook/edit tests and obsolete-flag rejection tests passed. The installed binary was rebuilt successfully; restoring the current saved session remains the final runtime step.

## 2026-10-31 — Keep target workspace guidance and decisions local

- Status: implemented.
- Rationale: unrestricted filesystem access must not cause a central session to apply one repository's conventions to another. Reuse local configuration and create `.agents` only for work, never for read-only inspection or every subdirectory.
- Affected: `workspace` inspect/prepare/record tool and optional `project_capability.directory`. Resolve a Git root (including worktrees), otherwise the explicit or nearest `.agents` workspace root. Inspect target ancestor/nested guidance and bounded decisions without starting MCP or scripts. Load plain `CLAUDE.md`, then `AGENTS.md`, then dot-directory guidance at each ancestor so more-specific sources remain later. Record meaningful dated status, rationale, affected areas, verification and outstanding work; use an existing local Markdown convention when present. No secrets, transcripts or routine logs.
- Verification / outstanding: target isolation, read-only inspection, explicit preparation, append/concurrency, validation and race tests passed. An independent read-only review found reversed plain/dot guidance precedence; a failing regression reproduced it and the shared loader was corrected. Final full-suite and vet checks passed. Startup/target loading share the same ordered, bounded loader.
