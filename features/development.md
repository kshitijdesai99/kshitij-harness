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

When a shell runner's foreground timeout is shorter than the check, run it in the background, capture output and exit status, and poll to completion. A runner timeout alone does not prove a test failure.

## Integrate and report

Review the combined diff for unrelated changes, duplication, and unnecessary complexity. Verify findings before making speculative fixes, and report checks actually run plus remaining uncertainty. Follow [documentation maintenance](CONTRIBUTING.md) for behavior changes and [restart instructions](startup-rebuild.md#restart-onto-a-new-build) when a changed runtime needs restarting; rebuilding does not update a live process.

[Feature index](README.md)
