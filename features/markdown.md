# Markdown and math

Present complete assistant replies as readable terminal documents.

Introduced **2026-10-05** · commit `51c87c7`.

## Current behavior

Capable interactive terminals render whole replies as Markdown, including readable math. Buffered replies flush at completion, failure, and cancellation so document-wide structures are not split into unrelated chunks. Explicit styles avoid terminal-background queries that could consume user input. Coordinated [console output](input.md#output-while-composing) preserves terminal detection, color, and width while restoring unfinished drafts around reply presentation. Plain output remains available when terminal rendering is unavailable; this is terminal presentation, not a general graphical document viewer. A [scoped reflow patch](../third_party/reflow/README.md) counts hyphen breakpoint widths before Glamour's second wrapping pass, preventing artificial one-word rows without joining source lines or removing inline styling.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-05 | Added whole-reply Markdown, math conversion, explicit styles, and reply flush boundaries. | `51c87c7` |
| 2026-10-16 | Distinguished commentary and turn statistics visually. | `efde4fb` |
| 2026-10-16 | Unified tool progress presentation. | `7cf70cf` |
| 2026-10-29 | Counted hyphen breakpoint widths to prevent spurious one-word rows during Markdown wrapping. | `a6799ed` |

## Inspect

- [Markdown renderer](../internal/terminal/markdown.go), [math conversion](../internal/terminal/math.go), and [reply boundaries](../internal/terminal/render.go)
- [Styled paragraph PTY/screen tests](../internal/terminal/test/styled_wrap_test.go), [Markdown tests](../internal/terminal/test/markdown_test.go), [math tests](../internal/terminal/test/math_test.go), and [provider flush tests](../internal/provider/codex/test/flush_test.go)
- [Presentation and context](architecture.md#presentation-and-context)

[Feature index](README.md)
