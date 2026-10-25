# Help

Discover commands and controls without starting a coding task.

Introduced 2026-09-28 · commit `84d64c8` (CLI flag help; chat and contextual help came later).

## Current behavior

Use `/help` for chat commands, `kh --help` for CLI usage, or `kh --help "QUESTION"` for contextual help. Contextual help uses the model but no local tools, saved chat history, or memory; it explains commands rather than inspecting or changing the workspace. It does not open or save a chat.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Initial CLI already exposed flag usage through Go's flag parser. | `84d64c8` |
| 2026-09-28 | Added an in-chat command listing through the slash-command fallback, including `/help`. | `be53ef2` |
| 2026-10-04 | Added standalone contextual one-off help without local tools or saved chat state. | `b0e6dc2` |
| 2026-10-23 | Updated contextual help for default interactive execution and `-p`; rejected mixing a help request with `-p`. | `bf2c75d` |

## Inspect

- [Contextual help](../internal/help/help.go), [CLI](../cmd/kh/main.go), and [help tests](../cmd/kh/test/help_test.go).
- [Essential usage](../README.md#use).

[Feature index](README.md)
