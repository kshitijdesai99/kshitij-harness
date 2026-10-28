# Visual companion prototype

A standalone marimo learning-rate experiment, not a kh integration. See the
[capability guide](../../features/visual-companion.md) for requirements, usage,
limitations, evidence, and history.

With `uv` installed, launch from the repository root:

```sh
XDG_STATE_HOME="$PWD/examples/visual-companion/.runtime/state" \
  UV_CACHE_DIR="$PWD/examples/visual-companion/.runtime/uv-cache" \
  MPLCONFIGDIR="$PWD/examples/visual-companion/.runtime/matplotlib" \
  uv run --no-project --with marimo marimo run --sandbox examples/visual-companion/companion.py
```

Open the printed local URL; stop with Ctrl-C. First launch needs network access.
Local runtime state and dependency caches stay in the ignored `.runtime/` directory.
