# Visual companion prototype

Explore an explanation interactively alongside chat using a standalone marimo notebook. This example is not a kh runtime feature or integration.

Introduced in the **2026-10-29 initial shipping change**; no earlier implementation commit is recorded.

## Availability

The optional [example](../examples/visual-companion/companion.py) runs separately in a browser. It requires `uv`, Python 3.11+, and network access on first launch to obtain dependencies. The script declares marimo and matplotlib dependencies; sandbox mode installs them in an isolated environment. No kh login or credentials are needed.

## Use

From the repository root:

```sh
XDG_STATE_HOME="$PWD/examples/visual-companion/.runtime/state" \
  UV_CACHE_DIR="$PWD/examples/visual-companion/.runtime/uv-cache" \
  MPLCONFIGDIR="$PWD/examples/visual-companion/.runtime/matplotlib" \
  uv run --no-project --with marimo marimo run --sandbox examples/visual-companion/companion.py
```

Open the local URL printed by marimo; stop the server with Ctrl-C. Replace `marimo run` with `marimo edit` to inspect or change cells. Keep local runtime state and dependency caches under the ignored `examples/visual-companion/.runtime/` directory, as in the command above.

The learning-rate slider recomputes 20 gradient-descent updates for **f(x) = x²**, starting at **x = 4**. Two plots show the parameter trajectory and log-scale loss, accompanied by an explanation of the selected rate.

Try these values to inspect the five regimes:

| Learning rate η | Expected result |
|---|---|
| 0.20 | Approach zero without crossing it |
| 0.50 | Reach zero in one update |
| 0.90 | Oscillate with shrinking magnitude |
| 1.00 | Alternate between +4 and −4 without improving loss |
| 1.10 | Oscillate with growing magnitude |

## Limitations

- No connection to kh, chat synchronization/history, agent tools, or kh-driven page navigation is implemented. Use chat and the notebook independently.
- This is one fixed quadratic demonstration, not a general optimization environment. Its learning-rate thresholds do not apply universally.
- The loss plot floors zero at 10⁻³⁰ for display; the reported loss uses the computed value.

## Evidence

- [Notebook source](../examples/visual-companion/companion.py): dependency metadata, slider, update rule, plots, and reactive explanation.
- [Example entry point](../examples/visual-companion/README.md) and [ignored generated files](../examples/visual-companion/.gitignore).
- Initial shipping verification: changing η from 0.20 to 1.10 updated both plots and the explanation without browser page errors; the screenshots were visually inspected. This is a manual smoke check, not automated regression coverage.
- [Regression checks](../examples/visual-companion/test/check_companion.py) exercise the public notebook `app.run()` API: the default value, all five rates above, trajectories, explanations, plotted series, and log-scale loss. They do not replace browser interaction checks.

Run the checks from the repository root:

```sh
XDG_STATE_HOME="$PWD/examples/visual-companion/.runtime/state" \
  UV_CACHE_DIR="$PWD/examples/visual-companion/.runtime/uv-cache" \
  MPLCONFIGDIR="$PWD/examples/visual-companion/.runtime/matplotlib" \
  uv run --no-project --with marimo --with matplotlib python examples/visual-companion/test/check_companion.py
```

## History

| Date | Change | Commit |
|---|---|---|
| 2026-10-29 | Initial standalone marimo demo and shipping documentation; no kh runtime integration. | This release (initial shipping change) |

[Feature index](README.md)
