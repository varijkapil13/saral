#!/usr/bin/env python3
"""Tests for scripts/benchgate.py.

    python3 scripts/benchgate_test.py

A gate nobody has run against a planted regression is a claim, not a gate, so
every case here either plants one and reads the message, or plants the noise a
shared runner produces and insists the gate stays quiet. Standard library only:
CI runs this before it runs the gate, on a runner with nothing but Go on it.
"""

import contextlib
import io
import os
import shutil
import sys
import tempfile
import unittest

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, SCRIPTS)
sys.dont_write_bytecode = True

import benchgate  # noqa: E402

MODULE = "example.test/app"

# One guard reading one benchmark, which is what makes that benchmark gated.
GUARD = """//go:build !race

package view

func TestBudget_ScrollingCostsTheFrame(t *testing.T) {
\tbig := testing.Benchmark(BenchmarkScroll)
\tsmall := testing.Benchmark(BenchmarkScroll20)
}
"""


def table(unit, rows):
    out = [",base.txt,,head.txt,,,", ",%s,CI,%s,CI,vs base,P" % (unit, unit)]
    out.extend(rows)
    out.append("geomean,1,,1,,+0.00%,")
    out.append("")
    return out


def csv_for(pkg, units):
    """A benchstat CSV over one package. units maps a unit to its data rows."""
    out = ["goos: linux", "goarch: amd64", "pkg: %s" % pkg, "cpu: fake"]
    for unit, rows in units.items():
        out.extend(table(unit, rows))
    return "\n".join(out) + "\n"


def steady(name, base, head, delta="~", p="p=1.000 n=6"):
    return "%s,%s,0%%,%s,0%%,%s,%s" % (name, base, head, delta, p)


class Gate(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="benchgate-")
        self.addCleanup(shutil.rmtree, self.dir, True)
        with open(os.path.join(self.dir, "go.mod"), "w") as f:
            f.write("module %s\n\ngo 1.26\n" % MODULE)
        os.makedirs(os.path.join(self.dir, "internal", "view"))
        with open(os.path.join(self.dir, "internal", "view", "budget_test.go"), "w") as f:
            f.write(GUARD)
        self.pkg = MODULE + "/internal/view"

    def run_gate(self, csv_text, **kw):
        path = os.path.join(self.dir, "cmp.csv")
        with open(path, "w") as f:
            f.write(csv_text)
        argv = ["--csv", path, "--root", self.dir]
        for key, val in kw.items():
            argv += ["--" + key, str(val)]
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = benchgate.main(argv)
        return code, out.getvalue()

    def both_scrolls(self, **units):
        """Both guarded benchmarks present, steady, plus whatever is overridden."""
        rows = {}
        for unit, default in (("allocs/op", "1"), ("B/op", "6156"), ("sec/op", "3.1e-06")):
            rows[unit] = units.get(unit, [
                steady("Scroll-4", default, default),
                steady("Scroll20-4", default, default),
            ])
        return csv_for(self.pkg, rows)


class TestItCatchesARegression(Gate):
    def test_a_scroll_that_stopped_hitting_its_memo(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "1", "42", "+4100.00%", "p=0.002 n=6"),
            steady("Scroll20-4", "1", "1"),
        ]}))
        self.assertEqual(code, 1, out)
        self.assertIn("::error::", out)
        self.assertIn("view.BenchmarkScroll allocs/op: 1 -> 42", out)

    def test_bytes_a_frame_growing_past_the_tolerance(self):
        code, out = self.run_gate(self.both_scrolls(**{"B/op": [
            steady("Scroll-4", "6156", "9000", "+46.20%", "p=0.002 n=6"),
            steady("Scroll20-4", "6156", "6156"),
        ]}))
        self.assertEqual(code, 1, out)
        self.assertIn("view.BenchmarkScroll B/op", out)

    def test_a_budgeted_benchmark_that_did_not_run_on_the_branch(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "1", "1"),
            "Scroll20-4,1,0%",
        ]}))
        self.assertEqual(code, 1, out)
        self.assertIn("reported no allocs/op on this branch", out)

    def test_a_package_that_holds_budgets_and_reached_no_comparison(self):
        code, out = self.run_gate(csv_for(MODULE + "/internal/other", {
            "allocs/op": [steady("Something-4", "1", "1")],
        }))
        self.assertEqual(code, 1, out)
        self.assertIn("holds budgets and contributed no benchmark", out)

    def test_an_empty_comparison_is_not_a_pass(self):
        code, out = self.run_gate("goos: linux\n")
        self.assertEqual(code, 1, out)
        self.assertIn("no comparison rows", out)


class TestItStaysQuiet(Gate):
    def test_the_same_code_twice(self):
        code, out = self.run_gate(self.both_scrolls())
        self.assertEqual(code, 0, out)
        self.assertNotIn("::error::", out)
        self.assertIn("No gated path allocates more", out)

    # The measurement this whole rule rests on: two runs of one commit disagreed
    # about wall clock by up to 821%, with benchstat calling it significant.
    def test_wall_clock_that_a_shared_runner_invented(self):
        code, out = self.run_gate(self.both_scrolls(**{"sec/op": [
            steady("Scroll-4", "2.48e-04", "2.28e-03", "+821.16%", "p=0.002 n=6"),
            steady("Scroll20-4", "3.1e-06", "3.1e-06"),
        ]}))
        self.assertEqual(code, 0, out)
        self.assertNotIn("::error::", out)

    def test_a_rise_benchstat_calls_noise(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "451", "1250", "~", "p=0.310 n=6"),
            steady("Scroll20-4", "1", "1"),
        ]}))
        self.assertEqual(code, 0, out)
        self.assertNotIn("::error::", out)

    def test_a_rise_inside_the_tolerance(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "1758", "1900", "+8.08%", "p=0.002 n=6"),
            steady("Scroll20-4", "1", "1"),
        ]}))
        self.assertEqual(code, 0, out)

    def test_an_improvement(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "42", "1", "-97.62%", "p=0.002 n=6"),
            steady("Scroll20-4", "1", "1"),
        ]}))
        self.assertEqual(code, 0, out)

    def test_a_benchmark_no_guard_reads_is_reported_and_not_gated(self):
        rows = {
            "allocs/op": [
                steady("Scroll-4", "1", "1"),
                steady("Scroll20-4", "1", "1"),
                steady("Something-4", "10", "40", "+300.00%", "p=0.002 n=6"),
            ],
        }
        code, out = self.run_gate(csv_for(self.pkg, rows))
        self.assertEqual(code, 0, out)
        self.assertIn("::warning::", out)
        self.assertIn("reported and not gated", out)

    def test_a_benchmark_the_branch_introduced(self):
        code, out = self.run_gate(self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "1", "1"),
            "Scroll20-4,,,1,0%",
        ]}))
        self.assertEqual(code, 0, out)
        self.assertIn("new on this branch", out)


class TestItReadsTheTree(Gate):
    def test_a_guard_reading_a_closure_leaves_the_package_watched(self):
        with open(os.path.join(self.dir, "internal", "view", "budget_test.go"), "w") as f:
            f.write("//go:build !race\n\npackage view\n\n"
                    "func TestBudget_X(t *testing.T) {\n"
                    "\tbig := testing.Benchmark(func(b *testing.B) { scroll(b, 2000) })\n}\n")
        reads, packages = benchgate.guarded_benchmarks(
            benchgate.pathlib.Path(self.dir), MODULE)
        self.assertEqual(reads, {})
        self.assertIn(self.pkg, packages)

    def test_the_tolerance_is_a_flag_and_not_a_constant(self):
        planted = self.both_scrolls(**{"allocs/op": [
            steady("Scroll-4", "1000", "1050", "+5.00%", "p=0.002 n=6"),
            steady("Scroll20-4", "1", "1"),
        ]})
        self.assertEqual(self.run_gate(planted)[0], 0)
        self.assertEqual(self.run_gate(planted, tolerance=1)[0], 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
