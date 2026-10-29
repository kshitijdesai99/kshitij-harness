# reflow v0.3.0 (local patch)

These production Go sources, `go.mod`, and `LICENSE` are copied from
`github.com/muesli/reflow` v0.3.0. Unused upstream examples, images, and tests are
not included. Keep this replacement scoped; do not edit the module cache.

The sole behavior patch is in `wordwrap/wordwrap.go`: count the display width of
a written breakpoint in `lineLen`, using the existing runewidth dependency.
Upstream omits this count for hyphens. Glamour's paragraph wrap can consequently
exceed its width; its later document wrap splits off the overflow word while
preserving the earlier newline, creating a spurious one-word row.

Coverage lives in `internal/terminal/test/styled_wrap_test.go` in the kh module.
Remove the replacement when an adopted upstream version contains the fix.
