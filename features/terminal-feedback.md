# Activity and message styling

Distinguish conversation roles and show what is working without exposing a persistent event log.

Introduced **2026-10-01** · commit `c00bc3d` (query/response colors; activity indicators followed).

## Current behavior

Terminal feedback includes activity indicators, distinct commentary/tool/peer styling, and clickable URLs. Plain output and `NO_COLOR` are supported. Presentation depends on terminal capabilities; these signals are not a durable event trace or an aggregate monitoring dashboard.

## Subdued harness output

Routine notices, actions, commentary, turn statistics, startup banners and peer text use subdued, non-bold tones rather than competing with the main reply. Harness text uses the existing muted gray (ANSI 90); worker-result blocks and peer messages retain subdued amber/brown (ANSI 137). Human input uses blue (ANSI 34) in live display and replay; assigned-task banners use bold green (ANSI 1;32), and final replies retain their [Markdown styling and two-column inset](markdown.md). Warnings and approval prompts retain a brighter diagnostic tone.

New direct-child conclusions appear as **WORKER RESULT — name**, with task identity and a truncation label when applicable. The renderer writes the original **Task** and **Result** together in one atomic, draft-safe block, always visible without collapse or navigation, without summarizing it or flushing an in-progress reply. It does not submit input or change the user's draft. See [result delivery and its limits](native-agents.md#visible-worker-results); this is not a transcript viewer.

## Assigned task startup banner

Descendants with an initial task print **ASSIGNED TASK — agent name** and the full assignment once, before activity setup, project/MCP startup notices, and model/tool work. The label and task use explicit bold green (ANSI 1;32), distinct from blue human queries and ordinary replies. Original newlines are preserved; long text wraps naturally in the terminal rather than being truncated to the registry's task summary. Redirected output, `NO_COLOR`, and `TERM=dumb` retain readable plain text without ANSI styling.

This is a startup scrollback banner, **not a pinned header**: later output can scroll it out of view. Both default headless workers and interactive `keep` descendants get it; interactive descendants do not echo a duplicate initial query. Ordinary root task display is unchanged. Launcher auto-rebuild and earlier session/memory setup can precede the banner.

## Live descendant counts

Inside tmux, every ecosystem agent's pane label shows `name (N - M)` for a manager or `name (N - W)` for a worker, for example `main (5 - M)` or `tests (0 - W)`. N counts **all live descendants**, including managers, not just direct children or worker-role agents, and excludes the agent itself. The count follows current ownership links independently of role: a manager demoted to worker can still have descendants. All ecosystem panes show zero and their role when they have no live descendants, including leaf workers.

The suffix uses the same live states as [capacity accounting](worker-lifecycle.md#roles-and-shared-capacity): reserved, starting, running, idle and stopping. Completed, failed and cancelled agents are excluded. Confirmed exits are reconciled before counting; an ambiguous process identity is not treated as an exit.

Counts and role labels refresh automatically about once per second, including while the chat is idle and after role switches; no new user message or `agents` call is required. Process-probe errors leave uncertain agents counted. A failed count query or tmux update can leave the last displayed count in place rather than presenting an unknown count as zero, so the display can be stale during errors.

The tmux tab displays the **active pane's** count and role beside the existing window name, not necessarily the root's or a fixed window owner's. Custom raw `window_name` values are preserved; the suffix is presentation, not part of an agent's logical address. Existing window-status styling is retained by decorating top-level `#W` or `#{window_name}` tokens only. Nested format expressions are left untouched; a shell segment (`#(...)`) and the remaining format tail are conservatively left untouched too. A custom format without an eligible name token has no tab suffix. This is tmux pane/tab feedback, not a separate tree view or dashboard.

On normal shutdown, kh restores the activity label's unsuffixed name and attempts to clear the pane count only if it still owns the pane. The composed window-status format remains installed but is inert for panes without a count, avoiding replacement of another pane's decoration or later theme changes.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-01 | Distinguished queries and responses with terminal colors. | `c00bc3d` |
| 2026-10-04 | Added activity feedback for running work. | `c8de1bf` |
| 2026-10-16 | Styled commentary and turn statistics separately. | `efde4fb` |
| 2026-10-16 | Unified tool and peer-message styling. | `7cf70cf` |
| 2026-10-16 | Made chat URLs clickable. | `cf2024c` |
| 2026-10-31 | Added idle-refreshing pane/tab suffixes counting all live descendants, excluding self, then role labels (`N - M`/`N - W`) for every ecosystem pane including leaf workers. | Current change |
| 2026-11-05 | Added a full, bold-cyan assigned-task startup banner for descendants, before model/tool work, without pinning or duplicate query output. | Current change |
| 2026-11-06 | Subdued routine harness output and added distinct worker-result blocks while preserving reply, input and diagnostic styling. | Current change |
| 2026-11-06 | Restored routine harness output to muted gray, superseding its yellow selection while retaining amber/brown for worker results and peer messages. | Current change |
| 2026-11-06 | Superseded cyan queries/assigned-task banners with blue human queries (live and replay) and bold-green assignments; paired original Task + Result output. | Current change |

## Inspect

- [Renderer](../internal/terminal/render.go), [activity](../internal/terminal/activity.go), and [colors](../internal/terminal/color.go)
- [Startup banner ordering tests](../cmd/kh/test/task_banner_test.go) and [paired Task + Result renderer tests](../internal/terminal/test/agent_task_result_test.go)
- [Color tests](../internal/terminal/test/color_test.go) and [activity tests](../internal/terminal/test/activity_test.go)
- Live-count [registry query](../internal/ecosystem/registry.go) and [runtime refresh/tmux display](../cmd/kh/ecosystem_titles.go)
- Descendant-count [registry tests](../internal/ecosystem/test/live_descendants_test.go) and [title tests](../internal/terminal/test/live_descendants_test.go)
- [Presentation and context](architecture.md#presentation-and-context)

[Feature index](README.md)

## 2026-11-06 — Subdue routine harness output and distinguish worker results

- Status: implemented; routine yellow selection superseded by the gray restoration below.
- Rationale: Reduce competition with primary replies while keeping human input, assignments and actionable diagnostics distinct; worker conclusions need a labeled, non-summary presentation.
- Affected: internal/terminal/color.go, internal/terminal/render.go, internal/terminal/markdown.go; current behavior documented above.
- Verification / outstanding: Before the gray restoration below, the full Go suite, focused renderer/feed/CLI checks and preview build passed on 2026-11-06; see [result-delivery verification](native-agents.md#2026-11-06--observe-bounded-direct-child-results-without-transcript-or-model-turn-injection). Manual visual inspection remains unrun; external review is paused and incomplete.

## 2026-11-06 — Restore muted gray for routine harness output

- Status: implemented
- Rationale: Supersedes the routine yellow selection above with existing muted gray (ANSI 90) so harness text recedes behind replies; other styling is unchanged.
- Affected: terminal palette and color assertions; current behavior, evolution and release note.
- Verification / outstanding: Terminal package tests, focused assigned-task banner and completion-observer CLI tests, preview build and diff whitespace check passed. Full repository suite was not rerun; manual visual inspection remains unrun and external review remains paused.

## 2026-11-06 — Distinguish blue human queries from bold-green assignments

- Status: implemented
- Rationale: Supersedes the prior cyan query and bold-cyan assigned-task choices while preserving that history. Blue human queries use ANSI 34 in live output and replay; AssignedTask uses ANSI 1;32. Harness gray, result/peer amber, response inset and diagnostic styles remain unchanged.
- Affected: Terminal palette, query replay and assigned-task presentation; current behavior and evolution above.
- Verification / outstanding: Final full Go suite, focused terminal/PTY and completion-observer/startup-banner CLI tests, ecosystem store tests and preview build passed on 2026-11-06; see [Task + Result verification](native-agents.md#2026-11-06--pair-original-tasks-with-visible-worker-results). Manual visual inspection remains unrun; external review is paused and incomplete.
