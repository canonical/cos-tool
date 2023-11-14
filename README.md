# cos-tool

[![Release](https://github.com/canonical/cos-tool/actions/workflows/release.yaml/badge.svg)](https://github.com/canonical/cos-tool/actions/workflows/release.yaml)
[![Discourse Status](https://img.shields.io/discourse/status?server=https%3A%2F%2Fdiscourse.charmhub.io&style=flat&label=CharmHub%20Discourse)](https://discourse.charmhub.io)

Transforms PromQL/LogQL expressions on the fly, and validates that Alert rules
can be loaded successfully by either Prometheus or Loki.

## Usage

Given the expression
```
job:request_latency_seconds:mean5m{job=\"myjob\"} > 0.5
```

### PromQL
Running:

```bash
$ ./cos-tool transform \
    --label-matcher juju_model=lma \
    --label-matcher juju_model_uuid=12345 \
    --label-matcher juju_application=proxy \
    --label-matcher juju_unit=proxy/1 \
    "job:request_latency_seconds:mean5m{job=\"myjob\"} > 0.5"
```

Would output

```
job:request_latency_seconds:mean5m{job="myjob",juju_application="proxy",juju_model="lma",juju_model_uuid="12345",juju_unit="proxy/1"} > 0.5
```

### LogQL

Running:

```bash
$ ./cos-tool transform \
    --format logql \
    --label-matcher juju_model=lma \
    --label-matcher juju_model_uuid=12345 \
    --label-matcher juju_application=proxy \
    --label-matcher juju_unit=proxy/1 \
    'rate({filename="myfile"}[1m])'
```

Would output

```
rate({filename="myfile",juju_application="proxy",juju_model="lma",juju_model_uuid="12345",juju_unit="proxy/1"}[1m])
```

### Alert Rule validation

Alert rules in either Loki or Prometheus syntax can be validated directly by running:

```
$ ./cos-tool [-f logql] validate rule_file.yaml [rule_file2.yaml ...]
```

If it's valid, no output will occur, and the return code will be zero.

If there are validation failures, they will be printed, and you will get a nonzero recdoe.
