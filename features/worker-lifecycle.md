# Worker lifecycle and policy

Reduce supervision overhead while retaining failed tasks for inspection.

Introduced **2026-09-29** · commit `277c243` (worker launch and auto-policy inheritance).

## Current behavior

- Successful one-shot workers close their panes; failures remain visible. Set `keep` when spawning to leave a worker open for follow-up messages.
- Workers inherit active [model and effort](model-settings.md), auto-approval, and [sandbox policy](shell-sandbox.md), **not directory grants**. See [write approvals](write-approvals.md).
- Shared files require [manager/peer coordination](native-agents.md#coordination-diagram); automatic pane closure is not proof that a worker’s changes are correct.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added worker launch with inherited auto-approval policy. | `277c243` |
| 2026-10-05 | Made successful workers exit after reporting, retained failures, and added `keep`. | `b658fe2` |
| 2026-10-16 | Added session-scoped outside-folder grants, not inherited by workers. | `09b6775` |
| 2026-10-17 | Unified automatic command and folder approvals under inherited auto policy. | `79f1ce2` |
| 2026-10-23 | Made interactive chats and auto approval the defaults. | `bf2c75d` |

## Inspect

[Coordinator](../cmd/kh/agents.go) · [Policy tests](../cmd/kh/test/auto_test.go)

[Feature index](README.md)
