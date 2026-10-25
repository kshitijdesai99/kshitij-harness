# Multiline drafts, paste, and history

Compose longer requests and recall earlier input in the interactive terminal.

Introduced 2026-10-01 · commit `b3d387a` (query history; multiline drafts came later).

## Current behavior

Shift+Enter or Ctrl-J adds a line to a draft; arrow keys recall previous input. Paste handling supports composing longer requests. Shift+Enter depends on terminal support, so Ctrl-J is the alternative when the modified key is unavailable.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-01 | Added interactive query history with arrow-key recall. | `b3d387a` |
| 2026-10-01 | Moved console input into the terminal layer and forwarded raw-mode Ctrl-C to turn cancellation. | `60e2bd1` |
| 2026-10-10 | Added Shift+Enter/Ctrl-J multiline drafts and refined paste handling. | `ded650e` |

## Inspect

- [Input implementation](../internal/terminal/input.go), [input tests](../internal/terminal/test/input_test.go), and [paste tests](../internal/terminal/test/paste_test.go).
- [Essential input controls](../README.md#use).

[Feature index](README.md)
