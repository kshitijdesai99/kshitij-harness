# Startup rebuild

Use current local sources at session startup without replacing already-running chats.

Introduced **2026-10-04** · commit `c8de1bf` (explicit rebuild; automatic startup rebuild followed on 2026-10-10).

## Current behavior

Normal session startup rebuilds and re-executes the invoked executable. `--rebuild` builds and exits; utility commands do not trigger the startup rebuild. This requires the checkout, Go, and a writable executable directory. `KH_SOURCE_DIR` locates moved sources; `KH_AUTO_REBUILD=0` skips automatic rebuilding.

Failed builds stop startup without replacing the old binary. Existing chats keep their running build: restart and resume saved history to use an update. Rebuilding alone does not update a live process.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-04 | Added explicit rebuilding of the invoked executable, preserving the old binary on build failure. | `c8de1bf` |
| 2026-10-10 | Added automatic session-start rebuild/re-exec and the opt-out environment setting. | `56b393b` |

## Inspect

- [Rebuild implementation](../cmd/kh/rebuild.go)
- [Rebuild tests](../cmd/kh/test/rebuild_test.go)
- [Updates and restart](../manual/safety-and-sessions.md#updates-and-restart)

[Feature index](README.md)
