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


## 2026-10-31 — Recursive agents share one ecosystem registry and capacity budget

- Status: implemented
- Rationale: The approved model keeps the user-facing root a fixed manager while descendants start as workers and may switch their own role before recursively delegating. A single SQLite registry and persisted live-agent limit per ecosystem prevent recursive per-manager capacity multiplication. Ownership links organize work; same-ecosystem participants can discover, message, inspect shared metadata/conclusions and stop agents without exposing private provider histories. New roots initialize agents.max_live (default 50 including root); roots refresh the persisted limit on genuine user turns and resume via SetMax, while descendants and peer messages cannot override it. Lower limits do not kill existing agents; there is no dedicated live-resize tool or CLI.
- Affected: internal/ecosystem, internal/tools/agents.go, internal/config, cmd/kh ecosystem/agent/chat wiring and tests; go.mod marks the existing x/sys v0.37.0 dependency direct for Darwin process identity without version or go.sum changes; features/native-agents.md, worker-lifecycle.md, architecture.md, features/README.md, diagrams/agent-coordination.svg, release-notes/2026-10-31.md.
- Runtime contracts: Reserved/starting/running/idle/stopping count; heartbeat age does not release slots, unknown launch/exit identities remain counted. Root stop covers the ecosystem; individual non-root stop reparents surviving children. Documentation includes the actual registry path, bounded first-line task_summary, pre-config/rebuild admission claim, error-only failed panes and macOS/Linux exact process identity. This consolidates the former same-day documentation-alignment record; baseline release items remain preserved.
- Verification / outstanding: Final stable-source `go test ./...`, `go vet ./...` and requested filtered ecosystem/native-agent/tmux/fixture race checks passed after the test-only fixture isolation fix; supplemental unfiltered ecosystem/agent race suites also passed. Linux amd64 ecosystem tests and cmd/kh crosscompiled successfully (not executed on Linux). `go list -mod=readonly ./...`, gofmt checks on all 21 changed tracked/untracked Go files and git diff --check passed. Independent native-worker documentation audit found no concrete mismatches; 154 local link occurrences including 2 Markdown anchors across 6 changed Markdown files passed. The parent rendered and inspected the final canonical SVG at full size. Claude independent review is blocked by expired OAuth, not a completed review. Parent exact-session restart/restore remains pending; no commit or push was performed in final verification.


## 2026-10-31 — Keep root conversation replies private from ecosystem inspection

- Status: implemented
- Rationale: Automatic shared conclusions represent delegated task results, not ordinary root conversation turns. Only descendants publish bounded final responses or detailed task-failure diagnostics, including descendants promoted to manager. Root failures expose generic operational status only. Explicit peer messages remain intentionally shared.
- Affected: cmd/kh/ecosystem.go, cmd/kh/main.go, features/worker-lifecycle.md, release-notes/2026-10-31.md
- Verification / outstanding: The fake-provider root-private/descendant-shared regression passed in the final full CLI suite and requested race run, alongside final full-suite and vet checks. Root/descendant publication guards and documentation were inspected. Claude independent review remains blocked by expired OAuth. Parent exact-session restart/restore and commit remain pending; this verification did not restart the parent.


## 2026-10-31 — Isolate CLI test entry from ambient ecosystem identity

- Status: implemented
- Rationale: Final verification from inside kh reproduced fixture startup failures because temporary-HOME CLI processes inherited the verifier's ecosystem identity. Clear KH_ECOSYSTEM_ID, KH_ECOSYSTEM_AGENT_ID and KH_ECOSYSTEM_TOKEN once in package TestMain; intentional ecosystem tests set their own values afterward. Preserve production admission checks and the existing fixture helper/regression.
- Affected: cmd/kh/test/cli_isolation_test.go
- Verification / outstanding: Initial full suite failed with ecosystem agent not found; an environment-clean rerun passed. After the approved test-only TestMain fix, the full suite passed from the original ambient kh environment, along with vet, targeted CLI/native-agent races and supplemental unfiltered ecosystem/agent races. No production source changed in this fix; parent exact-session restore remains pending.


## 2026-10-31 — Show live descendant counts beside parent pane and tab names

- Status: implemented
- Rationale: The approved display counts all live descendants through current ownership links, excluding self, rather than only direct children or only managers. Roots/current managers show zero before spawning; historical-parent workers retain zero after descendants finish. A one-second background refresh works while idle, preserving custom raw window names and composing the active pane's count into existing name-bearing tmux status formats. Count/probe/update errors are conservative rather than falsely reporting zero. Shutdown restores the unsuffixed activity label and attempts to clear only its owned pane count; composed window formats remain inert without a pane count instead of overwriting another pane or later theme configuration.
- Affected: cmd/kh/ecosystem_titles.go and main.go; internal/ecosystem registry/lifecycle; internal/terminal activity; associated test subpackages; features/terminal-feedback.md as display owner with native-agents, worker-lifecycle and tmux links; feature index and release-notes/2026-10-31.md. No new diagram or server.
- Verification / outstanding: Final post-parser-fix `go test ./...`, `go vet ./...`, unfiltered `go test -race ./internal/ecosystem/... ./internal/terminal/...` and CLI race tests matching `TestLiveDescendants|TestEcosystem|TestTmuxAgents|TestCLIFixtureIsolation` with `-count=1` passed. All tracked/untracked Go files plus go.mod/go.sum stayed hash-stable throughout verification; gofmt, `go list -mod=readonly ./...` and `git diff --check` passed. Source-checked the recursive query, idle refresh, title locking, cleanup and exact top-level-only format decoration; the canonical guide now notes nested-expression and shell-tail limitations. All 101 local links (including 10 Markdown anchors) across seven changed Markdown files passed; private fixture tmux servers were cleaned. Native review/simplification completed and its concrete nested-theme parser finding was fixed and regression-tested; no external Claude review completed. Logs and final report: `/Users/kshitijdesai/.kh/artifacts/live-count-20261031`. Parent exact-session restart/restore and runtime proof remain pending: these checks certify current source, not the still-running older root build. No commit, push or parent restart was performed.
