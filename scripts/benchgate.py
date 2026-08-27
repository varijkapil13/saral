#!/usr/bin/env python3
"""Fail a pull request that makes a budgeted path allocate more than it did.

Reads `benchstat -format csv` over two runs of the benchmark suite — one on the
branch, one on the base commit — and applies the rule docs/PERFORMANCE.md
states: a significant increase of more than the tolerance in `allocs/op` or
`B/op` on a benchmark a `TestBudget_*` guard reads fails the build. Wall-clock
figures are printed and never gated, because two runs of the same code on a
shared runner disagree about them by up to eight hundred per cent.

Usage:
    benchgate.py --csv cmp.csv --root . [--tolerance 10] [--summary FILE]
"""

import argparse
import csv
import os
import pathlib
import re
import sys

GATED_UNITS = ("allocs/op", "B/op")
REPORTED_UNITS = ("sec/op",)

# The name a guard hands testing.Benchmark. A closure has no name, so a path
# guarded only through one is invisible here — which is what
# TestBudget_TheRegressionGateWatchesEveryGuardedBenchmark exists to say.
GUARD_READS = re.compile(r"testing\.Benchmark\(\s*(Benchmark\w+)\s*\)")
PROCS_SUFFIX = re.compile(r"-\d+$")
DELTA = re.compile(r"^([+-])([\d.]+)%$")


class Row:
    def __init__(self, pkg, unit, name, base, head, delta, p):
        self.pkg = pkg
        self.unit = unit
        self.name = name
        self.base = base
        self.head = head
        self.delta = delta
        self.p = p

    @property
    def benchmark(self):
        return "Benchmark" + PROCS_SUFFIX.sub("", self.name)

    @property
    def top(self):
        return self.benchmark.split("/", 1)[0]

    @property
    def where(self):
        return "%s.%s" % (self.pkg.rsplit("/", 1)[-1], self.benchmark)

    def rise(self):
        """The increase benchstat called significant, as a percentage, or None."""
        m = DELTA.match(self.delta)
        if m is None or m.group(1) != "+":
            return None
        return float(m.group(2))


def module_path(root):
    for line in (root / "go.mod").read_text().splitlines():
        if line.startswith("module "):
            return line.split()[1]
    sys.exit("no module line in go.mod")


def skip_dir(name):
    return (
        name in ("testdata", "vendor")
        or name.startswith(".")
        or name.startswith("_")
    )


def guarded_benchmarks(root, module):
    """The benchmarks a TestBudget_* guard reads, and the packages holding budgets.

    A package with a guard file but no name in it guards through closures only,
    which the gate cannot select by name — it is still a package the comparison
    has to reach.
    """
    reads, packages = {}, set()
    for path in sorted(root.rglob("*budget_test.go")):
        rel = path.relative_to(root)
        if any(skip_dir(part) for part in rel.parts[:-1]):
            continue
        pkg = module
        if str(rel.parent) != ".":
            pkg += "/" + rel.parent.as_posix()
        packages.add(pkg)
        for name in GUARD_READS.findall(path.read_text()):
            reads.setdefault(name, set()).add(pkg)
    return reads, packages


def parse(path):
    """The comparison rows benchstat wrote, one per benchmark per unit."""
    rows, pkg, unit = [], "", None
    with open(path, newline="") as fh:
        for rec in csv.reader(fh):
            if not rec:
                unit = None
                continue
            if len(rec) == 1:
                if rec[0].startswith("pkg: "):
                    pkg = rec[0].split(None, 1)[1].strip()
                continue
            if len(rec) > 1 and rec[1] in GATED_UNITS + REPORTED_UNITS:
                unit = rec[1]
                continue
            if unit is None or rec[0] in ("", "geomean"):
                continue
            base = rec[1] if len(rec) > 1 else ""
            head = rec[3] if len(rec) > 3 else ""
            delta = rec[5] if len(rec) > 5 else ""
            p = rec[6] if len(rec) > 6 else ""
            rows.append(Row(pkg, unit, rec[0], base, head, delta, p))
    return rows


def value(raw):
    try:
        return float(raw)
    except ValueError:
        return None


def annotate(kind, message):
    print("::%s::%s" % (kind, message))


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--csv", required=True)
    ap.add_argument("--root", default=".")
    ap.add_argument("--tolerance", type=float, default=10.0)
    ap.add_argument("--summary")
    args = ap.parse_args(argv)

    root = pathlib.Path(args.root).resolve()
    module = module_path(root)
    guarded, guarded_pkgs = guarded_benchmarks(root, module)
    rows = parse(args.csv)

    if not rows:
        annotate("error", "benchstat produced no comparison rows, so nothing was compared")
        return 1

    failures, warnings, notes = [], [], []

    # A package with guards whose benchmarks did not reach the comparison is the
    # silent hole: the lane went green having measured nothing there.
    seen_pkgs = {row.pkg for row in rows}
    for pkg in sorted(guarded_pkgs - seen_pkgs):
        failures.append(
            "%s holds budgets and contributed no benchmark to the comparison. Either its "
            "benchmarks did not build or did not run, so the gate passed having measured "
            "nothing there." % pkg
        )

    gated_names = set()
    for row in rows:
        watched = row.pkg in guarded.get(row.top, set())
        if watched:
            gated_names.add((row.pkg, row.top))

        if row.unit not in GATED_UNITS:
            continue
        if not row.head:
            if watched:
                failures.append(
                    "%s is read by a budget guard and reported no %s on this branch. "
                    "A budgeted path the gate cannot see is a budgeted path the gate does "
                    "not hold." % (row.where, row.unit)
                )
            continue
        if not row.base:
            if watched:
                notes.append(
                    "%s is new on this branch, so its %s has no baseline to be held against yet"
                    % (row.where, row.unit)
                )
            continue

        rise = row.rise()
        if rise is None or rise <= args.tolerance:
            continue

        base, head = value(row.base), value(row.head)
        detail = "%s %s: %s -> %s, +%.1f%% (%s)" % (
            row.where, row.unit,
            ("%g" % base) if base is not None else row.base,
            ("%g" % head) if head is not None else row.head,
            rise, row.p or "no p-value",
        )
        if watched:
            failures.append(
                detail + ". A budget guard reads this benchmark, and %s is deterministic here — "
                "the same code reports the same figure run after run. If the branch changed what "
                "the benchmark measures, say so in the description; otherwise this is the "
                "regression docs/PERFORMANCE.md asks the gate to stop." % row.unit
            )
        else:
            warnings.append(detail + ". No budget guard reads this benchmark, so it is reported and not gated.")

    for name, pkgs in sorted(guarded.items()):
        for pkg in sorted(pkgs):
            if (pkg, name.split("/", 1)[0]) not in gated_names:
                failures.append(
                    "%s reads %s and no row for it reached the comparison, so the gate is not "
                    "watching a path a budget guard holds." % (pkg, name)
                )

    lines = []
    lines.append("### Allocation regressions against the base commit")
    lines.append("")
    lines.append(
        "%d benchmark comparisons read over %d packages, %d benchmarks of them read by a budget "
        "guard. Gated on `allocs/op` and `B/op` at %g%%; wall clock is reported and never gated."
        % (len(rows), len(seen_pkgs), len(gated_names), args.tolerance)
    )
    lines.append("")
    for label, items in (("Regressions", failures), ("Outside the budgeted set", warnings), ("New", notes)):
        if not items:
            continue
        lines.append("**%s**" % label)
        lines.append("")
        for item in items:
            lines.append("- %s" % item)
        lines.append("")
    if not failures and not warnings:
        lines.append("No gated path allocates more than it did on the base commit.")
        lines.append("")

    if args.summary:
        with open(args.summary, "a") as fh:
            fh.write("\n".join(lines) + "\n")

    for item in warnings:
        annotate("warning", item)
    for item in failures:
        annotate("error", item)

    print("\n".join(lines))
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
