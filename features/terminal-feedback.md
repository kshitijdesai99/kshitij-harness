# Activity and message styling

Distinguish conversation roles and show what is working without exposing a persistent event log.

Introduced **2026-10-01** · commit `c00bc3d` (query/response colors; activity indicators followed).

## Current behavior

Terminal feedback includes activity indicators, distinct commentary/tool/peer styling, and clickable URLs. Plain output and `NO_COLOR` are supported. Presentation depends on terminal capabilities; these signals are not a durable event trace or an aggregate monitoring dashboard.

## Live descendant counts

Inside tmux, a parent agent's pane label shows a suffix such as `dummy (5)`. It counts **all live descendants**, not just direct children, and excludes the agent itself. The count follows current ownership links, not manager/worker role: a manager demoted to worker can still be a parent. The root and current managers show `(0)` even before spawning; workers with no descendant history have no suffix. A worker that remains a historical parent keeps `(0)` after its descendants finish.

The suffix uses the same live states as [capacity accounting](worker-lifecycle.md#roles-and-shared-capacity): reserved, starting, running, idle and stopping. Completed, failed and cancelled agents are excluded. Confirmed exits are reconciled before counting; an ambiguous process identity is not treated as an exit.

Counts refresh automatically about once per second, including while the chat is idle; no new user message or `agents` call is required. Process-probe errors leave uncertain agents counted. A failed count query or tmux update can leave the last displayed count in place rather than presenting an unknown count as zero, so the display can be stale during errors.

The tmux tab displays the **active pane's** count beside the existing window name, not necessarily the root's count or a fixed window owner's count. Custom raw `window_name` values are preserved; the suffix is presentation, not part of an agent's logical address. Existing window-status styling is retained by decorating top-level `#W` or `#{window_name}` tokens only. Nested format expressions are left untouched; a shell segment (`#(...)`) and the remaining format tail are conservatively left untouched too. A custom format without an eligible name token has no tab suffix. This is tmux pane/tab feedback, not a separate tree view or dashboard.

On normal shutdown, kh restores the activity label's unsuffixed name and attempts to clear the pane count only if it still owns the pane. The composed window-status format remains installed but is inert for panes without a count, avoiding replacement of another pane's decoration or later theme changes.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-01 | Distinguished queries and responses with terminal colors. | `c00bc3d` |
| 2026-10-04 | Added activity feedback for running work. | `c8de1bf` |
| 2026-10-16 | Styled commentary and turn statistics separately. | `efde4fb` |
| 2026-10-16 | Unified tool and peer-message styling. | `7cf70cf` |
| 2026-10-16 | Made chat URLs clickable. | `cf2024c` |
| 2026-10-31 | Added idle-refreshing parent pane/tab suffixes counting all live descendants, excluding self. | Current change |

## Inspect

- [Renderer](../internal/terminal/render.go), [activity](../internal/terminal/activity.go), and [colors](../internal/terminal/color.go)
- [Color tests](../internal/terminal/test/color_test.go) and [activity tests](../internal/terminal/test/activity_test.go)
- Live-count [registry query](../internal/ecosystem/registry.go) and [runtime refresh/tmux display](../cmd/kh/ecosystem_titles.go)
- Descendant-count [registry tests](../internal/ecosystem/test/live_descendants_test.go) and [title tests](../internal/terminal/test/live_descendants_test.go)
- [Presentation and context](architecture.md#presentation-and-context)

[Feature index](README.md)
