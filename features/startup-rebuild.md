# Startup rebuild

Use current local sources at session startup without replacing already-running chats.

Introduced **2026-10-04** · commit `c8de1bf` (explicit rebuild; automatic startup rebuild followed on 2026-10-10).

## Current behavior

Normal session startup rebuilds and re-executes the invoked executable from local sources. This requires Go, the kh checkout, and a writable executable directory. Set `KH_SOURCE_DIR=/path/to/kshitij-harness` if sources move, or `KH_AUTO_REBUILD=0` to skip rebuilding. Failed builds stop startup without replacing the old binary. Utility commands do not trigger this startup rebuild.

`kh --rebuild` explicitly rebuilds and exits; it takes no other flags or task. Automatic rebuilds use the invoked binary's source checkout, not an unrelated working checkout; explicit rebuilds prefer the current checkout unless `KH_SOURCE_DIR` is set.

## Restart onto a new build

Existing chats keep their running build. Rebuilding alone does not replace a live process, and attaching to its [tmux session](tmux-chats.md) does not update it.

1. Stop active work and exit the old chat with Ctrl-D on empty input.
2. Start kh again in the same working folder, using `kh -s SESSION_ID` to select the [saved conversation](saved-sessions.md); `kh -r` selects the newest by creation ID.
3. Startup rebuilds and runs the new executable unless disabled above.

Restarting also ends process-lifetime [folder grants](write-approvals.md). Change history belongs in the [release notes](../release-notes/).

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-10-04 | Added explicit rebuilding of the invoked executable, preserving the old binary on build failure. | `c8de1bf` |
| 2026-10-10 | Added automatic session-start rebuild/re-exec and the opt-out environment setting. | `56b393b` |

## Inspect

- [Rebuild implementation](../cmd/kh/rebuild.go)
- [Rebuild tests](../cmd/kh/test/rebuild_test.go)

[Feature index](README.md)
