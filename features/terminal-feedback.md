# Activity and message styling

Distinguish conversation roles and show what is working without exposing a persistent event log.

Introduced **2026-10-01** · commit `c00bc3d` (query/response colors; activity indicators followed).

## Current behavior

Terminal feedback includes activity indicators, distinct commentary/tool/peer styling, and clickable URLs. Plain output and `NO_COLOR` are supported. Presentation depends on terminal capabilities; these signals are not a durable event trace or an aggregate monitoring dashboard.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-01 | Distinguished queries and responses with terminal colors. | `c00bc3d` |
| 2026-10-04 | Added activity feedback for running work. | `c8de1bf` |
| 2026-10-16 | Styled commentary and turn statistics separately. | `efde4fb` |
| 2026-10-16 | Unified tool and peer-message styling. | `7cf70cf` |
| 2026-10-16 | Made chat URLs clickable. | `cf2024c` |

## Inspect

- [Renderer](../internal/terminal/render.go), [activity](../internal/terminal/activity.go), and [colors](../internal/terminal/color.go)
- [Color tests](../internal/terminal/test/color_test.go) and [activity tests](../internal/terminal/test/activity_test.go)
- [Presentation and context](../manual/architecture.md#presentation-and-context)

[Feature index](README.md)
