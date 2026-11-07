# Development

Make focused changes that preserve public behavior and can be checked independently.

## Before editing

Find the owning page through the [feature index](README.md); use [architecture](architecture.md) to locate the implementation layer. Keep provider protocols, terminal presentation, tool access policy, and memory concerns separated.

## Build and test

From the repository root:

```bash
go build ./cmd/kh
go test ./...
```

Use targeted package tests during development, then broader checks appropriate to the change. Apply `gofmt` to changed Go files and inspect `git diff --check`. Documentation-only changes need link, anchor, and factual checks; do not imply application tests ran if they did not.

All `*_test.go` files belong in existing `test/` subpackages, not production package roots. Test public behavior rather than exposing internals only for tests.

CLI fixtures must provide explicit non-terminal stdin, such as `strings.NewReader("")`, and set `KH_AUTO_REBUILD=0` unless testing rebuild behavior. See [mode fixtures](../cmd/kh/test/mode_test.go) and [rebuild tests](../cmd/kh/test/rebuild_test.go).

The CLI package's [TestMain and isolation helper](../cmd/kh/test/cli_isolation_test.go) clear ambient `KH_ECOSYSTEM_ID`, `KH_ECOSYSTEM_AGENT_ID` and `KH_ECOSYSTEM_TOKEN`: a verifier's ecosystem identity is not an admission ticket for fixture processes. Intentional ecosystem tests set their own identity afterward. Isolate both HOME and cwd where project discovery must not read real ancestor configuration; keep production admission checks intact. This test-only isolation was added on 2026-10-31 after inherited identity reproduced fixture startup failures.

When a shell runner's foreground timeout is shorter than the check, run it in the background, capture output and exit status, and poll to completion. A runner timeout alone does not prove a test failure.

## Before every commit

Run these review gates automatically as part of repository development, without waiting for a user to invoke a skill or request the review. They apply to every commit, including documentation-only changes; they do not grant permission to commit or push.

1. **Correctness and style:** independently review the complete intended diff for concrete bugs, regressions, unrelated changes, and convention violations. Verify findings before fixing them, then run the [checks appropriate to the change](#build-and-test).
2. **Standalone generalization/overfitting review:** assign a separate reviewer before committing. Check for plugin-, vendor-, or task-specific assumptions and unnecessary special cases. Evaluate unrelated examples and add or verify representative coverage where behavior is affected, so success on the motivating example is not mistaken for a general solution. Remove confirmed overfitting and recheck affected behavior; preserve genuinely required domain rules. Do not replace a few cases with an overengineered framework or generic workflow layer.
3. **Simplification and closure:** remove unnecessary duplication and complexity without speculative rewrites. Resolve confirmed findings and rerun affected checks; if the intended diff changes after review, review the changed scope again before committing. Report the generalization review explicitly alongside correctness, style, simplification, actual checks, and remaining uncertainty in the final verification evidence.

## Integrate and report

Before pushing, independently review concrete bugs in the combined diff, verify fixes and tests, and remove unnecessary drift; pre-commit review does not replace this gate. When using verified-state push delegation, follow the existing [verified Git push contract](shell.md#verified-git-push) rather than substituting an ordinary push if its preconditions fail.

Follow [documentation maintenance](CONTRIBUTING.md) for behavior changes and [restart instructions](startup-rebuild.md#restart-onto-a-new-build) when a changed runtime needs restarting; rebuilding does not update a live process.

## Maintenance history

**2026-11-07 — Implemented:** made [generalization review](#before-every-commit) an automatic pre-commit development convention rather than an optional skill, alongside correctness, style and simplification. This guide owns the gate; the existing [agent reading order](../AGENTS.md#reading-order) already points here. The separate pre-push gate remains in place. No hooks, scripts or generic workflow framework were introduced. Scoped diff and local links/anchors were checked, and `git diff --check` passed; application tests were not run for this documentation-only change. No runtime work is required.

[Feature index](README.md)
