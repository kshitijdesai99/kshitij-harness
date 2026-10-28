"""Regression checks for the standalone notebook's public app.run API."""

import math
from pathlib import Path
import runpy
from types import SimpleNamespace
import unittest


class CompanionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = runpy.run_path(str(Path(__file__).resolve().parents[1] / "companion.py"))["app"]

    def test_default(self):
        _, definitions = self.app.run()
        self.assertAlmostEqual(definitions["learning_rate"].value, 0.2)
        self.assertAlmostEqual(definitions["trajectory"][-1], 4 * 0.6**20)

    def test_learning_rate_regimes(self):
        for eta, explanation in (
            (0.2, "without crossing"),
            (0.5, "exactly on zero"),
            (0.9, "oscillations shrink"),
            (1.0, "loss does not improve"),
            (1.1, "method diverges"),
        ):
            with self.subTest(eta=eta):
                _, definitions = self.app.run(
                    defs={"learning_rate": SimpleNamespace(value=eta)}
                )
                trajectory = definitions["trajectory"]
                expected = [4 * (1 - 2 * eta) ** step for step in range(21)]
                self.assertEqual(len(trajectory), 21)
                for actual, value in zip(trajectory, expected):
                    self.assertTrue(math.isclose(actual, value, rel_tol=1e-12, abs_tol=1e-12))
                self.assertIn(explanation, definitions["behavior"])
                axes = definitions["axes"]
                self.assertEqual(list(axes[0].lines[0].get_ydata()), trajectory)
                self.assertEqual(
                    list(axes[1].lines[0].get_ydata()),
                    [max(x * x, 1e-30) for x in trajectory],
                )
                self.assertEqual(axes[1].get_yscale(), "log")


if __name__ == "__main__":
    unittest.main()
