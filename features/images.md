# Visual input

Let the model inspect actual image contents rather than infer them from filenames or metadata.

Introduced **2026-09-30** · commit `c344a45`.

## Current behavior

Attach a local file for the next message with `/image PATH`, use `/image` for the macOS clipboard, or ask the model to inspect a file with `view_image`, which sends actual image contents to the active model. Local PNG, JPEG, WebP, and GIF files up to 10 MB are supported. `view_image` accepts repository-relative, absolute, and `~/` paths, including files outside the project subject to OS read permissions; it does not read URLs or the clipboard. Clipboard capture requires Swift on macOS. Image input does not itself verify the model's interpretation.

## Evolution

| Date | Change | Commit |
|---|---|---|
| 2026-09-30 | Added interactive image and clipboard attachments. | `c344a45` |
| 2026-09-30 | Simplified image encoding return values. | `1205bdd` |
| 2026-10-17 | Added model-invoked local image viewing. | `8c523b0` |

## Inspect

- [Image tool](../internal/tools/image.go) and [image loader](../internal/image/image.go)
- [Tool tests](../internal/tools/test/image_test.go) and [attachment tests](../cmd/kh/test/image_paste_test.go)
- [Input controls](input.md)

[Feature index](README.md)
