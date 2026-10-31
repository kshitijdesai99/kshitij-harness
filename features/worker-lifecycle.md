# Worker lifecycle and policy

Reduce supervision overhead while retaining failed-task diagnostics for inspection.

Introduced **2026-09-29** · commit `277c243` (worker launch and auto-policy inheritance).

## Current behavior

- Successful one-shot workers close their panes; failed one-shot panes retain error output, not a live kh process. Set `keep` when spawning to retain a live interactive worker for follow-up messages, including after a task error.
- Workers inherit active [model and effort](model-settings.md) and [command auto-approval policy](write-approvals.md). Filesystem access is unrestricted by kh, subject to OS permissions.
- Shared files require [manager/peer coordination](native-agents.md#coordination-diagram); automatic pane closure is not proof that a worker’s changes are correct.

## Roles and shared capacity

The [root remains manager](native-agents.md); descendants default to worker and can switch their own role. Role changes do not consume a new slot or multiply the limit. Parent links remain ownership metadata, not a restriction on cross-branch coordination. The [pane/tab label](terminal-feedback.md#live-descendant-counts) shows the current role and a live descendant count that follows these links independently of role.

Set the shared budget in `~/.kh/config.json`:

```json
{ "agents": { "max_live": 50 } }
```

The default is **50 live agents including the root**, with a minimum of 1. A new root initializes its ecosystem from this configuration. The root refreshes the persisted limit from configuration on genuine user turns and when resuming a saved root; descendants and peer messages do not override it. Lowering the limit never kills existing agents: further admission waits for occupancy to fall below the limit or for the root to apply a higher configured limit. There is no dedicated live limit-change tool or CLI. Independent new/forked chats create their own ecosystems, rather than joining the delegation tree. Resuming a saved root reuses its ecosystem only after its previous process is confirmed absent; surviving descendants still count toward admission.

`stats` exposes occupancy and manager/worker counts to every participant. Admission reserves a slot transactionally before launch, and a child claims that reservation before configuration loading, startup rebuild or model work. Reserved, starting, running, idle and stopping agents all count. Switching roles does not free capacity; concurrent managers compete for the same remaining slots. At capacity, spawning returns an error but the orchestration tools remain available: inspect, wait or stop work, then retry after confirmed exit.

## Inspection and stopping

The shared registry persists at `~/.kh/ecosystem.db`. Safe inspection exposes coordination metadata, `task_summary` and bounded descendant task results, including descendant failure diagnostics, not full histories or pane output. A descendant still shares its final task result after promotion to manager. Ordinary root replies and detailed failure diagnostics remain private to the user-facing conversation; the root publishes only generic operational failure status automatically. The task summary contains only the first line of the trimmed delegated prompt, with whitespace normalized and a maximum of 280 runes (including an ellipsis when truncated); the raw multiline prompt is not shared registry metadata. Explicit `send` remains a peer message; it is not a separate conclusion-publishing API. Keep sensitive private context out of deliberately shared task summaries and final reports.

`stop` targets one agent by default, or its descendants too with `scope: subtree`. **Stopping the root stops the whole ecosystem**, even with the default scope. A stop request is not proof of exit and does not immediately release capacity. Process identity is checked before termination; unconfirmed exits and ambiguous launches remain counted. Reconciliation uses process identity rather than treating an old heartbeat alone as proof of death. An unclaimed startup can be cancelled after its launcher is proven dead; a reservation without launcher identity does not expire automatically. Stopping only a non-root agent reparents its surviving direct children to its parent; changing role alone leaves ownership links intact.

Exact process identity is supported on **macOS and Linux**: macOS combines PID, boot-session UUID and kernel microsecond start time; Linux combines PID, boot ID and `/proc` process start ticks. Other platforms return an unsupported-identity error rather than falling back to PID-only lifecycle checks.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added worker launch with inherited auto-approval policy. | `277c243` |
| 2026-10-05 | Made successful workers exit after reporting, retained failures, and added `keep`. | `b658fe2` |
| 2026-10-16 | Added session-scoped outside-folder grants, not inherited by workers. | `09b6775` |
| 2026-10-17 | Unified automatic command and folder approvals under inherited auto policy. | `79f1ce2` |
| 2026-10-23 | Made interactive chats and auto approval the defaults. | `bf2c75d` |
| 2026-10-31 | Removed directory grants and filesystem confinement; command approval remains inherited. | Current change |
| 2026-10-31 | Added mutable descendant roles and one shared live-agent budget including the root. | Current change |

## Inspect

[Coordinator](../cmd/kh/agents.go) · [Runtime wiring](../cmd/kh/ecosystem.go) · [Registry lifecycle](../internal/ecosystem/lifecycle.go) · [Policy tests](../cmd/kh/test/auto_test.go)

[Feature index](README.md)
