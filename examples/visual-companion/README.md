# Visual companion example

A marimo learning-rate experiment. For kh's native `visual` tool, lifecycle,
requirements, and limitations, see the [capability guide](../../features/visual-companion.md).
The manual commands below run **unmanaged**: kh does not stop these servers.
Native launches instead resolve PEP 723 dependencies with `uv run --with-requirements`
and use `marimo run --watch --no-sandbox` to render immediately, watch file edits, and preserve guardian ownership. Marimo's
`--sandbox` environment launcher is separate from kh's OS Bash sandbox and approvals.

## Standalone launch

With `uv` installed, prepare a copy outside the repository from the repository root:

```sh
workdir="$(mktemp -d "${TMPDIR:-/tmp}/kh-visual.XXXXXX")"
mkdir -p "$workdir/test"
cp examples/visual-companion/companion.py "$workdir/companion.py"
cp examples/visual-companion/test/check_companion.py "$workdir/test/check_companion.py"
export UV_CACHE_DIR="$HOME/.kh/cache/visual/uv"
export XDG_STATE_HOME="$workdir/state" XDG_CONFIG_HOME="$workdir/config"
export MPLCONFIGDIR="$workdir/matplotlib" PYTHONPYCACHEPREFIX="$workdir/pycache"
cd "$workdir"
```

Then launch:

```sh
uv run --no-project --with marimo marimo run --sandbox --headless "$workdir/companion.py"
```

Open the printed local URL yourself; stop with Ctrl-C. Nothing opens a browser tab
automatically. First launch needs network access. Replace `marimo run` with
`marimo edit` to edit the copied notebook. Generated files stay outside the source
checkout; the work directory is retained, and the uv cache is shared with native
launches. Remove the work directory yourself when no longer needed.

## Regression checks

After the same preparation, run:

```sh
uv run --no-project --with marimo --with matplotlib python "$workdir/test/check_companion.py"
```

These checks exercise notebook computation, not browser interaction or native
server ownership. See the [capability guide](../../features/visual-companion.md#evidence)
for coverage and history.
