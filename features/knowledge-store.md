# Scoped, typed knowledge

Reuse established lessons across chats without treating transcripts as permanent instructions.

Introduced **2026-09-29** · commit `14dd3e6`.

## Current behavior

- SQLite at `~/.kh/memory.db` stores repo/global instructions, facts, decisions, and gotchas, separately from saved conversations.
- Retrieved notes are reference data; current user requests take precedence. Workers share retrieval, but delegated prompts are not learned as user instructions.
- Background hooks selectively save knowledge; saving may decline or fail and is not an exhaustive archive. Keyword retrieval works by default; optional hybrid retrieval is a separate capability.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added scoped, database-backed knowledge and retrieval. | `14dd3e6` |
| 2026-10-05 | Replaced discovery/detail records with append-only notes, repo identity, and background saving. | `0a203a6` |
| 2026-10-16 | Added fact/decision note types and richer provenance. | `46c225b` |
| 2026-10-23 | Added database-native procedure references and inert assets. | `f802b27` |

## Inspect

[Store](../internal/memory/memory.go) · [Store tests](../internal/memory/test/memory_test.go) · [Database and learning details](../manual/memory.md#database-as-source-of-truth)

[Feature index](README.md)
