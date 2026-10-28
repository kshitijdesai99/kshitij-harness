# /// script
# requires-python = ">=3.11"
# dependencies = ["marimo>=0.19,<1", "matplotlib>=3.9,<4"]
# ///

import marimo

__generated_with = "0.25.2"
app = marimo.App(width="medium")


@app.cell
def _():
    import marimo as mo
    import matplotlib.pyplot as plt
    return mo, plt


@app.cell
def _(mo):
    mo.md(r"""
    # A visual companion to chat

    Chat is useful for asking questions and deciding what to try. This small,
    standalone notebook makes one idea tangible: **how does learning rate change
    gradient descent?** Move the slider and compare the resulting trajectories.

    We minimize $f(x)=x^2$, starting at $x_0=4$, using
    $x_{t+1}=x_t-\eta\,2x_t$. The minimum is at $x=0$.
    """)
    return


@app.cell
def _(mo):
    learning_rate = mo.ui.slider(
        start=0.05, stop=1.10, step=0.05, value=0.20,
        label="Learning rate (η)", show_value=True,
    )
    learning_rate
    return (learning_rate,)


@app.cell
def _(learning_rate):
    eta = learning_rate.value
    trajectory = [4.0]
    for _ in range(20):
        trajectory.append(trajectory[-1] * (1 - 2 * eta))
    return eta, trajectory


@app.cell
def _(plt, trajectory):
    fig, axes = plt.subplots(1, 2, figsize=(10, 3.6), layout="constrained")
    axes[0].plot(range(len(trajectory)), trajectory, "o-", markersize=4)
    axes[0].axhline(0, color="gray", linewidth=1, linestyle="--")
    axes[0].set(xlabel="Update", ylabel="x", title="Parameter trajectory")
    axes[1].semilogy(
        range(len(trajectory)), [max(x * x, 1e-30) for x in trajectory], "o-",
        markersize=4,
    )
    axes[1].set(xlabel="Update", ylabel="Loss x² (log scale)", title="Distance from the goal")
    for _axis in axes:
        _axis.grid(alpha=0.2)
    plt.close(fig)
    fig
    return


@app.cell
def _(eta, mo, trajectory):
    if eta < 0.5:
        behavior = "The steps approach zero without crossing it. Smaller steps converge more slowly."
    elif abs(eta - 0.5) < 1e-9:
        behavior = "One update lands exactly on zero for this particular quadratic."
    elif eta < 1 - 1e-9:
        behavior = "The steps overshoot zero, but the oscillations shrink: this still converges."
    elif abs(eta - 1) < 1e-9:
        behavior = "The steps bounce between +4 and −4. The loss does not improve."
    else:
        behavior = "The oscillations grow: the learning rate is too large and the method diverges."
    mo.md(f"""
    ### What happened?

    **η = {eta:.2f}** · After 20 updates: **x = {trajectory[-1]:.4g}**,
    **loss = {trajectory[-1] ** 2:.4g}**.

    {behavior} Every update multiplies x by **1 − 2η = {1 - 2 * eta:.2f}**.
    Convergence requires that multiplier's magnitude to be below 1.
    These thresholds apply to this example, not every optimization problem.
    The log plot floors zero loss at 10⁻³⁰ so it can be displayed.

    ---
    **Notebook vs. chat:** use chat to ask “why?” and choose the next experiment;
    use the notebook to change a parameter and see its consequences immediately.
    This prototype has no live connection to kh, no chat history, and no agent tools.
    """)
    return


if __name__ == "__main__":
    app.run()
