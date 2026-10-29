# Multiline drafts, paste, and history

Compose longer requests and recall earlier input in the interactive terminal.

Introduced 2026-10-01 · commit `b3d387a` (query history; multiline drafts came later).

## Current behavior

**Enter** sends the whole draft. **Shift+Enter** adds a line, shown with a `... ` continuation prompt; **Ctrl-J** is the fallback when the terminal cannot distinguish Shift+Enter. **Up/Down** recall and navigate input history. Multiline pasted content is a message, not a batch of slash commands. See [steering and stopping](steering.md) for controls during active work.

## Output while composing

Replies and chat notices clear the active editor before printing, then restore its prompt, draft, and cursor. This includes paste placeholders and the active `... ` continuation line; output does not submit the draft or expand its pastes. Both stdout replies and stderr notices/approval feedback share this coordination. [Markdown replies](markdown.md) still flush as complete documents and wrap to the terminal width, not the space left beside your draft.

## Peer messages and drafts

Incoming agent messages are delivered independently of the human editor. They do not append to or submit an unfinished draft, change its cursor position, enter its history, or become part of an active multiline draft or paste. Your draft is submitted only when you press Enter. Peer messages also bypass slash-command and approval-answer parsing.

Existing sender and receiver chats must [restart onto the updated build](startup-rebuild.md#restart-onto-a-new-build) to use isolated delivery. Updated senders reject receivers without live support instead of falling back to simulated typing. See the [agent coordination diagram](native-agents.md#coordination-diagram) for parent/peer delivery separate from the human draft.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-01 | Added interactive query history with arrow-key recall. | `b3d387a` |
| 2026-10-01 | Moved console input into the terminal layer and forwarded raw-mode Ctrl-C to turn cancellation. | `60e2bd1` |
| 2026-10-10 | Added Shift+Enter/Ctrl-J multiline drafts and refined paste handling. | `ded650e` |
| 2026-10-25 | Separated framed peer delivery from unfinished human drafts, paste/history, and approval parsing. | `dee1c86` |
| 2026-10-29 | Coordinated stdout/stderr chat output with the active editor while preserving terminal Markdown, paste placeholders, and cursor position. | Pending |

## Inspect

- [Input implementation](../internal/terminal/input.go), [coordinated output](../internal/terminal/output.go), [input tests](../internal/terminal/test/input_test.go), [output regression tests](../internal/terminal/test/output_test.go), and [paste tests](../internal/terminal/test/paste_test.go).
- [Chat modes](chat-modes.md) and [image attachments](images.md).

[Feature index](README.md)
