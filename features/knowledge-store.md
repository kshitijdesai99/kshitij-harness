# Scoped, typed knowledge

Reuse established lessons across chats without treating transcripts as permanent instructions.

Introduced **2026-09-29** · commit `14dd3e6`.

## Architecture

![Memory architecture and the database's role](../diagrams/memory-architecture.svg)

[Memory overview SVG](../diagrams/memory-architecture.svg): the shared database supplies recall and receives selective background learning. See the [system architecture](architecture.md#application-and-turn-loop) for surrounding components; this page owns storage details.

## Current behavior

SQLite at `~/.kh/memory.db` is the runtime source of truth for repo/global instructions, facts, decisions, and gotchas. Conversations live separately in `~/.kh/sessions`; documentation is not a second runtime knowledge store. Retrieved notes are reference data subordinate to current user requests.

| Table | Purpose |
|---|---|
| `notes` | Append-only topic versions, scope, provenance, replacement links, and forgotten markers. |
| `notes_fts` | FTS5 keyword index over note keys and text. |
| `notes_vectors` | Optional vector cache keyed by note and model/endpoint identity, with content hashes to detect changes. |
| `reference_payloads` | Full procedures, aliases, inert assets, portable IDs, versions, and import provenance linked to a note version. |

SQLite WAL supports shared reads while another process writes. See [version history](memory-history.md) for current-version and stale-write rules, [controls](memory-controls.md) for forgetting/purging, and [procedure loading](procedure-loading.md) for database-native workflows. [Learning hooks](learning-hooks.md) selectively retain knowledge; [keyword recall](keyword-recall.md) works without model downloads.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-29 | Added scoped, database-backed knowledge and retrieval. | `14dd3e6` |
| 2026-10-05 | Replaced discovery/detail records with append-only notes, repo identity, and background saving. | `0a203a6` |
| 2026-10-16 | Added fact/decision note types and richer provenance. | `46c225b` |
| 2026-10-23 | Added database-native procedure references and inert assets. | `f802b27` |

## Inspect

[Store/schema](../internal/memory/memory.go) · [Vector cache](../internal/memory/semantic.go) · [Reference transactions](../internal/memory/references.go) · [Store tests](../internal/memory/test/memory_test.go)

[Feature index](README.md)
