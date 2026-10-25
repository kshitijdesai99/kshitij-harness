# Diagram index

Find existing system views and their owning feature pages. See [visual explanation standards](../features/CONTRIBUTING.md#visual-explanations) for maintenance guidance.

## Existing diagrams

| SVG | Purpose | Owning feature page |
|---|---|---|
| [Agent coordination](agent-coordination.svg) | Manager/worker direction, messaging, and shared files. | [Native agents](../features/native-agents.md) |
| [Compaction lifecycle](compaction-lifecycle.svg) | Context replacement, persistence, and failure boundaries. | [Compaction](../features/compaction.md) |
| [Conversation lifecycle](conversation-lifecycle.svg) | Save, resume, attach, fork, and fresh-chat relationships. | [Saved sessions](../features/saved-sessions.md) |
| [Learning flow](learning-flow.svg) | Eligible events, background jobs, and guarded memory saves. | [Learning hooks](../features/learning-hooks.md) |
| [Memory architecture](memory-architecture.svg) | Answer-time recall and separate background learning. | [System architecture](../features/architecture.md) |
| [Procedure flow](procedure-flow.svg) | Learned references, explicit loading, and separate execution. | [Procedure loading](../features/procedure-loading.md) |
| [Provider state](provider-state.svg) | Provider-owned history, reply rollback, checkpoints, and replay. | [Provider state](../features/provider-state.md) |
| [Retrieval flow](retrieval-flow.svg) | Required recall, optional topic search, and bounded selection. | [Keyword recall](../features/keyword-recall.md) |
| [Turn steering](turn-steering.svg) | Turn execution, ordered tool results, and submitted steering. | [Execution](../features/execution.md) |
| [Write boundaries](write-boundaries.svg) | Approval, folder grants, shell sandboxing, and edit-path checks. | [Write approvals](../features/write-approvals.md) |

## Possible later views — not created

Edit-batch validation/write boundaries, memory-topic lifecycle, helper lifecycle, presentation flow, and startup-rebuild/settings propagation are candidates only. Simple commands, configuration, and release notes remain text; this is not a second feature inventory.
