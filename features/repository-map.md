# Repository map

Offer bounded initial project context so the model can locate relevant files.

Introduced 2026-09-28 · commit `0c70fd0`.

## Current behavior

Opt in through `map_cap`, whose default is `0` (off). The map supplies Git filenames and top-level Go symbols, not a full code index. Outside Git projects there is no map. Normal default operation inspects files on demand instead of enlarging the system prompt.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-28 | Added a capped repository map to initial context; initially enabled by default. | `0c70fd0` |
| 2026-09-28 | Removed the non-Git directory-walk fallback. | `6c5ea3e` |
| 2026-09-29 | Changed the default `map_cap` to `0`, favoring on-demand inspection. | `14dd3e6` |

## Inspect

- [Map implementation](../internal/repomap/repomap.go), [configuration](../internal/config/config.go), and [map tests](../internal/repomap/test/self_test.go).
- [Presentation and context architecture](../manual/architecture.md#presentation-and-context).

[Feature index](README.md)
