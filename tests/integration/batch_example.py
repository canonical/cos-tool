#!/usr/bin/env python3
"""Worked example of calling `cos-tool transform --batch` from Python.

This is the integration cos-tool's batch mode exists for: Juju charm code
transforming thousands of alert rule expressions per hook invocation, where one
subprocess per expression would cost more in process startup than in actual
work.

The file is both documentation and a test. `transform_batch` below is the whole
integration — copy it. Everything after it asserts the guarantees that make it
safe to rely on, so if cos-tool ever breaks one of them, this fails.

Standard library only, because charm hooks have no test dependencies available.

Usage:
    make build
    python3 tests/integration/batch_example.py [path/to/cos-tool]
"""

from __future__ import annotations

import json
import pathlib
import subprocess
import sys
import tempfile

# ──────────────────────────────────────────────────────────────────────
# The integration
# ──────────────────────────────────────────────────────────────────────


def transform_batch(cos_tool, rules, shared_matchers=None, fmt="promql"):
    """Transform many expressions in one cos-tool invocation.

    `rules` maps an opaque id to a `(expression, matchers)` pair. The id comes
    back untouched on each result, so it can be anything that identifies the
    rule on this side: a database key, a file path and index, an object id.

    `shared_matchers` are passed once on the command line and apply to every
    expression. Per-rule matchers override them key by key, so pass the labels
    that are constant for the whole batch here and only the ones that vary
    per rule in `rules` -- on a 50,000-line batch that is a real size saving.

    Returns the parsed result objects in input order.
    """
    with tempfile.TemporaryDirectory() as tmp:
        # A private directory per invocation. cos-tool overwrites --out
        # unconditionally, so the caller is responsible for picking a path it
        # owns and for cleaning both files up.
        requests = pathlib.Path(tmp) / "requests.jsonl"
        results = pathlib.Path(tmp) / "results.jsonl"

        # One JSON object per line. json.dumps escapes anything that would
        # otherwise break the line framing, including embedded newlines.
        with requests.open("w", encoding="utf-8") as f:
            for rule_id, (expr, matchers) in rules.items():
                request = {"id": rule_id, "expr": expr}
                if matchers:
                    request["matchers"] = matchers
                f.write(json.dumps(request) + "\n")

        argv = [
            str(cos_tool),
            "--format",
            fmt,
            "transform",
            "--batch",
            str(requests),
            "--out",
            str(results),
        ]
        for name, value in (shared_matchers or {}).items():
            argv += ["--label-matcher", f"{name}={value}"]

        # check=True is the important part: a non-zero exit means cos-tool did
        # not complete the batch, so the results file is unusable -- not a
        # partial result to be salvaged. A zero exit guarantees one result per
        # request, but says nothing about whether each expression was valid.
        #
        # Note there is no deadlock risk here, unlike piping a large batch
        # through stdin/stdout: the batch is exchanged as files, so writing it
        # all before reading any results is safe.
        subprocess.run(argv, check=True, capture_output=True, text=True)

        with results.open(encoding="utf-8") as f:
            return [json.loads(line) for line in f]


def example(cos_tool):
    """What a caller actually writes, end to end."""
    rules = {
        # Per-rule topology: these vary rule by rule, so they travel per record.
        "cpu-high": (
            "rate(cpu_seconds_total[5m]) > 0.9",
            {"juju_application": "worker", "juju_unit": "worker/0"},
        ),
        "disk-full": (
            "node_filesystem_avail_bytes / node_filesystem_size_bytes < 0.1",
            {"juju_application": "worker", "juju_unit": "worker/1"},
        ),
        # No per-rule matchers: inherits just the batch-wide ones.
        "target-down": ("up == 0", None),
        # Deliberately invalid, to show that one bad expression is survivable.
        "broken": ("rate(oops{{[5m])", None),
    }

    results = transform_batch(
        cos_tool,
        rules,
        # Constant for the whole batch, so passed once on the command line.
        shared_matchers={"juju_model": "cos", "juju_model_uuid": "deadbeef"},
    )

    applied, failed = {}, {}
    for result in results:
        # Always branch on status. A zero exit means every request was
        # answered, not that every answer succeeded.
        if result["status"] == "ok":
            applied[result["id"]] = result["expr"]
        else:
            # Non-ok results carry `error`, plus `line` to locate the input.
            failed[result["id"]] = result["error"]

    return applied, failed


# ──────────────────────────────────────────────────────────────────────
# Checks -- the documented guarantees, asserted
# ──────────────────────────────────────────────────────────────────────

CHECKS = []


def check(fn):
    """Register a check. Named so failures read as prose."""
    CHECKS.append(fn)
    return fn


@check
def the_example_runs(cos_tool):
    applied, failed = example(cos_tool)

    assert set(applied) == {"cpu-high", "disk-full", "target-down"}, applied
    assert set(failed) == {"broken"}, failed

    # Batch-wide matchers reach every expression...
    for expr in applied.values():
        assert 'juju_model="cos"' in expr, expr
        assert 'juju_model_uuid="deadbeef"' in expr, expr

    # ...and per-rule matchers reach only their own.
    assert 'juju_unit="worker/0"' in applied["cpu-high"]
    assert 'juju_unit="worker/0"' not in applied["disk-full"]
    assert "juju_unit" not in applied["target-down"]

    # The invalid expression failed on its own, without taking the batch down.
    assert "parse error" in failed["broken"], failed["broken"]


@check
def one_result_per_request_in_order(cos_tool):
    """The guarantee everything else is built on."""
    rules = {f"rule-{i}": (f'up{{job="j{i}"}} == 0', None) for i in range(500)}

    results = transform_batch(cos_tool, rules, {"juju_model": "cos"})

    assert len(results) == len(rules), f"{len(rules)} requests, {len(results)} results"
    assert [r["id"] for r in results] == list(rules), "results are out of order"

    # Correlating by id is what the id is for; order is a convenience on top.
    by_id = {r["id"]: r for r in results}
    assert len(by_id) == len(rules)
    assert 'job="j250"' in by_id["rule-250"]["expr"]


@check
def a_malformed_line_costs_only_itself(cos_tool):
    """One unreadable line must not cost the other 49,999 results."""
    with tempfile.TemporaryDirectory() as tmp:
        requests = pathlib.Path(tmp) / "requests.jsonl"
        results = pathlib.Path(tmp) / "results.jsonl"

        # Hand-written rather than via transform_batch, because the point is a
        # line that no correct caller would produce.
        requests.write_text(
            '{"id":"good-1","expr":"up == 0"}\n'
            "this is not json\n"
            '{"id":"no-expr"}\n'
            '{"id":"good-2","expr":"up == 1"}\n',
            encoding="utf-8",
        )

        subprocess.run(
            [
                str(cos_tool),
                "transform",
                "--batch",
                str(requests),
                "--out",
                str(results),
            ],
            check=True,
            capture_output=True,
            text=True,
        )

        parsed = [
            json.loads(line)
            for line in results.read_text(encoding="utf-8").splitlines()
        ]

    assert [r["status"] for r in parsed] == ["ok", "malformed", "malformed", "ok"], (
        parsed
    )

    # A malformed result reports its 1-based input line, so it can be found in
    # a large batch file even when the id was unreadable.
    assert parsed[1]["line"] == 2, parsed[1]
    assert parsed[2]["line"] == 3, parsed[2]
    assert "expr" in parsed[2]["error"], parsed[2]


@check
def failure_is_distinguishable_from_no_usable_results(cos_tool):
    """Per-expression failure is data; a non-zero exit means no results."""
    # An invalid expression: exit stays zero, the failure is in the file.
    results = transform_batch(cos_tool, {"bad": ("rate(oops{{[5m])", None)})
    assert results[0]["status"] == "error", results

    # A missing batch file: cos-tool could not complete, so it exits non-zero
    # and the results file must not be trusted.
    with tempfile.TemporaryDirectory() as tmp:
        out = pathlib.Path(tmp) / "results.jsonl"
        completed = subprocess.run(
            [
                str(cos_tool),
                "transform",
                "--batch",
                str(pathlib.Path(tmp) / "nope.jsonl"),
                "--out",
                str(out),
            ],
            capture_output=True,
            text=True,
        )

        assert completed.returncode != 0, (
            "a missing batch file must fail the invocation"
        )
        assert completed.stderr.strip(), "a failure must explain itself on stderr"

    # A usage error creates no file at all.
    with tempfile.TemporaryDirectory() as tmp:
        out = pathlib.Path(tmp) / "results.jsonl"
        completed = subprocess.run(
            [str(cos_tool), "transform", "--out", str(out)],  # --out without --batch
            capture_output=True,
            text=True,
        )

        assert completed.returncode != 0, "--out without --batch must fail"
        assert not out.exists(), "a usage error must not create the results file"


@check
def formats_can_be_mixed_and_variables_survive(cos_tool):
    """Per-record `format`, and Grafana template variables passing through."""
    with tempfile.TemporaryDirectory() as tmp:
        requests = pathlib.Path(tmp) / "requests.jsonl"
        results = pathlib.Path(tmp) / "results.jsonl"

        with requests.open("w", encoding="utf-8") as f:
            # Inherits the invocation's --format (promql).
            f.write(
                json.dumps({"id": "prom", "expr": "rate(metric[$__rate_interval])"})
                + "\n"
            )
            # Overrides it for this record alone.
            f.write(
                json.dumps(
                    {
                        "id": "loki",
                        "expr": 'sum(rate({job="nginx"}[5m]))',
                        "format": "logql",
                    }
                )
                + "\n"
            )

        subprocess.run(
            [
                str(cos_tool),
                "--format",
                "promql",
                "transform",
                "--batch",
                str(requests),
                "--out",
                str(results),
                "--label-matcher",
                "juju_model=cos",
            ],
            check=True,
            capture_output=True,
            text=True,
        )

        parsed = [
            json.loads(line)
            for line in results.read_text(encoding="utf-8").splitlines()
        ]

    by_id = {r["id"]: r for r in parsed}
    assert by_id["prom"]["status"] == "ok", by_id["prom"]
    assert by_id["loki"]["status"] == "ok", by_id["loki"]

    # Grafana variables are preserved for the dashboard to resolve at render time.
    assert "$__rate_interval" in by_id["prom"]["expr"], by_id["prom"]["expr"]
    assert 'juju_model="cos"' in by_id["loki"]["expr"], by_id["loki"]["expr"]


@check
def an_empty_batch_is_valid(cos_tool):
    """Nothing to do is a success, not an error -- worth not special-casing."""
    assert transform_batch(cos_tool, {}) == []


@check
def batch_support_can_be_probed(cos_tool):
    """Check for batch support before going to the trouble of writing a file."""
    completed = subprocess.run(
        [str(cos_tool), "transform", "--batch-version"],
        check=True,
        capture_output=True,
        text=True,
    )

    assert int(completed.stdout.strip()) >= 1, completed.stdout

    # An older cos-tool without batch mode rejects the flag and exits non-zero,
    # which is what makes this usable as a capability probe.


# ──────────────────────────────────────────────────────────────────────


def main(argv):
    cos_tool = pathlib.Path(argv[1] if len(argv) > 1 else "bin/cos-tool")

    if not cos_tool.exists():
        sys.exit(f"cos-tool not found at {cos_tool}; run `make build` first")

    failures = 0
    for fn in CHECKS:
        name = fn.__name__.replace("_", " ")
        try:
            fn(cos_tool)
        except AssertionError as err:
            failures += 1
            print(f"FAIL  {name}\n      {err}")
        except subprocess.CalledProcessError as err:
            failures += 1
            print(
                f"ERROR {name}\n      cos-tool exited {err.returncode}: {err.stderr.strip()}"
            )
        else:
            print(f"ok    {name}")

    print()
    if failures:
        print(f"{failures} of {len(CHECKS)} checks failed")
        return 1

    print(f"All {len(CHECKS)} checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
