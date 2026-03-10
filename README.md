# cos-tool

[![Release](https://github.com/canonical/cos-tool/actions/workflows/release.yaml/badge.svg)](https://github.com/canonical/cos-tool/actions/workflows/release.yaml)
[![Discourse Status](https://img.shields.io/discourse/status?server=https%3A%2F%2Fdiscourse.charmhub.io&style=flat&label=CharmHub%20Discourse)](https://discourse.charmhub.io)

Transforms PromQL/LogQL expressions on the fly, and validates that Alert rules
can be loaded successfully by either Prometheus or Loki.

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

### Grafana template variables

Grafana dashboard expressions often contain template variables such as `$job`, `${grouping}` or
`${metric:value}`. cos-tool preserves these variables while injecting the topology matchers, so
the transformed expression remains valid for Grafana to evaluate at render time.

#### Supported patterns

**PromQL**

| Pattern | Example |
|---|---|
| Label value | `{job="$job", instance=~"${instance}"}` |
| Duration / range | `rate(metric[$__rate_interval])` |
| Full metric name | `${metric_name}{label="value"}` |
| Metric name suffix | `http_requests${_total}{label="value"}` |
| Function name | `${fn:value}(metric[5m])` |
| Grouping label | `sum by ($grouping) (expr)`, `sum(expr) without ($exclude)` |
| Function argument | `topk($limit, metric{label="value"})` |

**LogQL**

| Pattern | Example |
|---|---|
| Label value | `{job="$job", instance=~"${instance}"}` |
| Duration / range | `rate({app="nginx"}[$__rate_interval])` |
| Grouping label | `sum by ($grouping) (rate({job="$job"}[5m]))` |
| Filter string | <code>|= "$pattern"</code> |

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

