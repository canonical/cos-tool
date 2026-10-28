# cos-tool

[![Release](https://github.com/canonical/cos-tool/actions/workflows/release.yaml/badge.svg)](https://github.com/canonical/cos-tool/actions/workflows/release.yaml)
[![Discourse Status](https://img.shields.io/discourse/status?server=https%3A%2F%2Fdiscourse.charmhub.io&style=flat&label=CharmHub%20Discourse)](https://discourse.charmhub.io)

Transforms PromQL/LogQL expressions on the fly, and validates that Alert rules
can be loaded successfully by either Prometheus or Loki.

Expressions can be transformed one at a time, or many thousands at a time via
[batch transform](#batch-transform).

## Installation

Download the latest binary from the [GitHub Releases page](https://github.com/canonical/cos-tool/releases),
or build from source:

```bash
make build   # produces ./bin/cos-tool
```

## Usage

### PromQL transform

Given the expression:

```
rate(http_requests_total{job="myjob"}[5m]) > 0.5
```

Running:

```bash
$ ./cos-tool --format promql transform \
    --label-matcher juju_model=cos \
    --label-matcher juju_model_uuid=12345 \
    --label-matcher juju_application=proxy \
    --label-matcher juju_unit=proxy/1 \
    -- 'rate(http_requests_total{job="myjob"}[5m]) > 0.5'
```

Outputs:

```
rate(http_requests_total{job="myjob",juju_application="proxy",juju_model="cos",juju_model_uuid="12345",juju_unit="proxy/1"}[5m]) > 0.5
```

### LogQL transform

```bash
$ ./cos-tool --format logql transform \
    --label-matcher juju_model=cos \
    --label-matcher juju_model_uuid=12345 \
    --label-matcher juju_application=proxy \
    --label-matcher juju_unit=proxy/1 \
    -- 'rate({filename="myfile"}[1m])'
```

Outputs:

```
rate({filename="myfile", juju_application="proxy", juju_model="cos", juju_model_uuid="12345", juju_unit="proxy/1"}[1m])
```

### Batch transform

Transforming one expression per invocation costs roughly 20ms in process startup
alone, which dominates when a caller has tens of thousands of alert rule
expressions to transform in one go. Batch mode transforms many expressions in a
single invocation:

```bash
$ ./cos-tool [--format promql|logql] transform \
    --batch requests.jsonl \
    --out results.jsonl \
    [--label-matcher juju_model=cos ...]
```

`--batch` and `--out` are required together, and cannot be combined with a
positional expression. Passing only one of them, or passing `--batch` alongside
an expression, is a usage error: cos-tool exits non-zero without creating any
file.

Both files are [JSONL](https://jsonlines.org/) — one JSON object per line.
cos-tool writes exactly one result line per request line, in input order.

#### Requests

| Field      | Required | Description                                                              |
| ---------- | -------- | ------------------------------------------------------------------------ |
| `id`       | yes      | Opaque caller-supplied identifier, returned unchanged on the result      |
| `expr`     | yes      | The expression to transform                                              |
| `matchers` | no       | Label matchers specific to this expression                               |
| `format`   | no       | `promql` or `logql`; defaults to the invocation's `--format`             |
| `version`  | no       | Batch interface version this request is written against; defaults to `1` |

Any `--label-matcher` flags act as **batch-wide defaults that records extend**.
The effective matcher set for a request is the `--label-matcher` set merged, per
key, with that record's `matchers` — the record wins on conflict. This lets you
pass the labels that are constant for the whole batch once on the command line,
and repeat only what actually varies per rule.

So given:

```bash
$ ./cos-tool transform \
    --batch requests.jsonl \
    --out results.jsonl \
    --label-matcher juju_model=cos \
    --label-matcher juju_model_uuid=12345
```

with `requests.jsonl`:

```jsonl
{"id":"rule-42","expr":"rate(http_requests_total{job=\"myjob\"}[5m]) > 0.5","matchers":{"juju_application":"proxy","juju_unit":"proxy/1"}}
{"id":"rule-43","expr":"sum(rate({job=\"nginx\"}[5m]))","matchers":{"juju_application":"nginx"},"format":"logql"}
{"id":"rule-44","expr":"up == 0"}
{"id":"rule-45","expr":"rate(bad{{[5m])"}
```

`results.jsonl` contains:

```jsonl
{"version":1,"id":"rule-42","status":"ok","expr":"rate(http_requests_total{job=\"myjob\",juju_application=\"proxy\",juju_model=\"cos\",juju_model_uuid=\"12345\",juju_unit=\"proxy/1\"}[5m]) > 0.5"}
{"version":1,"id":"rule-43","status":"ok","expr":"sum(rate({job=\"nginx\", juju_application=\"nginx\", juju_model=\"cos\", juju_model_uuid=\"12345\"}[5m]))"}
{"version":1,"id":"rule-44","status":"ok","expr":"up{juju_model=\"cos\",juju_model_uuid=\"12345\"} == 0"}
{"version":1,"id":"rule-45","status":"error","error":"1:10: parse error: unexpected left brace '{'"}
```

`rule-44` carries no `matchers` of its own, so it gets just the two batch-wide
defaults. `rule-43` is a LogQL expression in an otherwise PromQL batch.

A record can override an inherited default, but there is no way to _remove_ one —
if a label should not apply to every expression in the batch, carry it per record
instead of on the command line. A `null` matcher value is rejected as
`malformed` rather than treated as a removal, which keeps `null` available for
that meaning in a future interface version.

Both `--label-matcher` and `matchers` are optional. When neither supplies a
matcher, nothing is injected and the expression is simply parsed and re-emitted
in cos-tool's canonical form, which is a convenient way to bulk-validate
expressions.

#### Results

Every result carries `version` and `status`, plus the request's `id` whenever it
could be read. `version` is the interface version of the binary that produced
the result, not the version the request declared.

| `status`              | Meaning                                                                                                          | Additional fields                   |
| --------------------- | ---------------------------------------------------------------------------------------------------------------- | ----------------------------------- |
| `ok`                  | Transformed successfully                                                                                         | `expr` — the transformed expression |
| `error`               | The expression is invalid and could not be transformed                                                           | `error` — the parse error           |
| `malformed`           | The line could not be read as a request at all (invalid JSON, missing `id` or `expr`, wrong types, bad `format`) | `error`                             |
| `unsupported_version` | The request declares a `version` this binary does not implement                                                  | `error`                             |

Every failing result also carries `line`, the 1-based input line number, so you
can find the offending line in a large batch file even when the `id` could not
be recovered. Successful results omit it as redundant.

An invalid expression fails only its own record. One bad line does not cost you
the other results, which is why `malformed` is reported per record rather than
aborting the batch.

#### Exit status

The exit code reports only whether **cos-tool itself** completed — that it could
read the batch file, write the results file, and hit no internal error. It does
_not_ report whether every expression was valid.

- **Zero** — the batch completed. There is exactly one result line per request
  line. Individual failures are in the results file, so check each `status`.
- **Non-zero** — treat the results file as unusable, not as a partial result,
  even if some lines were written before the failure. Diagnostics go to stderr.

An empty batch file is valid: cos-tool writes an empty results file and exits
zero.

#### Calling from Python

A complete, runnable version of this is in
[`tests/integration/batch_example.py`](tests/integration/batch_example.py),
which doubles as a test of the guarantees below.

```python
import json, subprocess, tempfile, pathlib

with tempfile.TemporaryDirectory() as tmp:
    batch = pathlib.Path(tmp) / "requests.jsonl"
    out = pathlib.Path(tmp) / "results.jsonl"

    with batch.open("w") as f:
        for rule_id, expr, matchers in rules:
            f.write(json.dumps({"id": rule_id, "expr": expr, "matchers": matchers}) + "\n")

    subprocess.run(
        ["cos-tool", "--format", "promql", "transform",
         "--batch", str(batch), "--out", str(out),
         "--label-matcher", "juju_model=cos"],
        check=True,   # non-zero exit means no usable results
    )

    with out.open() as f:
        for line in f:
            result = json.loads(line)
            if result["status"] == "ok":
                apply_expression(result["id"], result["expr"])
            else:
                report_failure(result)
```

Notes for callers:

- `--out` is overwritten if it already exists, so use a path you own — a
  per-invocation temporary directory is the simplest choice. Cleaning up both
  files is the caller's responsibility.
- Because requests and results are exchanged as files rather than streamed over
  pipes, there is no risk of deadlocking against cos-tool by writing the whole
  batch before reading any results.
- `id` is opaque and is not checked for uniqueness. If you submit duplicate ids
  you will still get one result per request, but you will lose data when keying
  results by id.
- Records are independent lines, so neither side needs to hold the whole batch
  in memory. cos-tool imposes no line-length limit of its own; if you read
  results with one, make it generous enough for your longest expression plus
  its matchers.
- Blank lines are not skipped. Each one is reported as its own `malformed`
  result, so that there is always exactly one result per line, which lets you
  check the two files line up. A single trailing newline at the end of the
  batch file is normal and does not produce an extra result.
- Expressions and label values must be valid UTF-8.
- Result data only ever goes to the results file. stdout stays empty, and
  stderr carries diagnostics, so both are safe to log.

#### Checking for batch support

```bash
$ ./cos-tool transform --batch-version
1
```

This prints the highest batch interface version the binary implements and exits
zero, touching no files. An older cos-tool without batch mode rejects the flag
and exits non-zero, so this doubles as a capability probe before writing a batch
file.

The interface is designed to grow without breaking existing callers:
unrecognised fields are ignored on both sides, so new optional fields can be
added later. A request declaring a `version` newer than the binary supports is
reported as `unsupported_version` on that record rather than failing the batch.

### Grafana template variables

Grafana dashboard expressions often contain template variables such as `$job`, `${grouping}` or
`${metric:value}`. cos-tool preserves these variables while injecting the topology matchers, so
the transformed expression remains valid for Grafana to evaluate at render time.

This applies equally to single-expression and [batch](#batch-transform) transforms.

#### Supported patterns

**PromQL**

| Pattern            | Example                                                     |
| ------------------ | ----------------------------------------------------------- |
| Label value        | `{job="$job", instance=~"${instance}"}`                     |
| Duration / range   | `rate(metric[$__rate_interval])`                            |
| Full metric name   | `${metric_name}{label="value"}`                             |
| Metric name suffix | `http_requests${_total}{label="value"}`                     |
| Function name      | `${fn:value}(metric[5m])`                                   |
| Grouping label     | `sum by ($grouping) (expr)`, `sum(expr) without ($exclude)` |
| Function argument  | `topk($limit, metric{label="value"})`                       |

**LogQL**

| Pattern          | Example                                       |
| ---------------- | --------------------------------------------- |
| Label value      | `{job="$job", instance=~"${instance}"}`       |
| Duration / range | `rate({app="nginx"}[$__rate_interval])`       |
| Grouping label   | `sum by ($grouping) (rate({job="$job"}[5m]))` |
| Filter string    | `\|= "$pattern"`                              |

#### Known limitations

**Variable prefixes in metric names** (PromQL only) are not supported:

```promql
${prefix}_requests_total{label="value"}  # ❌ not supported — results in a parse error
```

Metric names should identify a specific measurement type. The base name is the semantic identity of
what's being measured. Variable prefixes would make this identity undefined, breaking the semantic
contract of metrics.
In practice, suffixes like `_total` or `_bytes` are conventional modifiers, but prefixes would be the metric name itself.

#### Examples

**Function-name variables** — a variable that resolves to a PromQL function name at render time:

```bash
$ ./cos-tool --format promql transform \
    --label-matcher juju_model=otelcol \
    -- 'sum(${metric:value}(otelcol_receiver_accepted_metric_points${suffix_total}{receiver=~"$receiver",job="$job"}[$__rate_interval])) by (receiver $grouping)'
```

Outputs:

```
sum by (receiver, $grouping) (${metric:value}(otelcol_receiver_accepted_metric_points${suffix_total}{job="$job",juju_model="otelcol",receiver=~"$receiver"}[$__rate_interval]))
```

**Grouping variables** in `by`/`without` clauses — commas between static labels and variables are
optional in Grafana, cos-tool normalises them automatically:

```bash
$ ./cos-tool --format logql transform \
    --label-matcher juju_model=cos \
    -- 'sum by ($grouping) (rate({job="$job"}[5m]))'
```

Outputs:

```
sum by($grouping)(rate({job="$job", juju_model="cos"}[5m]))
```

> **Note — `by`/`without` clause position:** PromQL and LogQL accept grouping clauses both before
> and after the aggregated expression (`sum by (...) (expr)` and `sum(expr) by (...)` are
> equivalent). cos-tool always emits the **prefix form** (`sum by (...) (expr)`) regardless of
> which form was used in the input. This is a cosmetic difference only — both forms are evaluated
> identically by Prometheus, Grafana and Loki.

### Alert rule validation

Alert rules in either Loki or Prometheus syntax can be validated by running:

```bash
$ ./cos-tool [-f logql] validate-rules rule_file.yaml [rule_file2.yaml ...]
```

If the file is valid, there is no output and the exit code is zero.

If there are validation failures, they are printed to stderr and the exit code is non-zero:

```
error validating rule_file.yaml: [5:15: group "test", rule 1, "BadExpr": could not parse expression: 1:11: parse error: unexpected left brace '{']
```
