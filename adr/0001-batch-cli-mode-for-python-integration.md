# 1. Batch CLI mode for high-volume Python integration

## Status

Accepted

## Context

`cos-tool` is consumed by Juju charm code (Python) to:

- inject Juju topology label matchers into PromQL/LogQL alert rule expressions
  (`transform`), and
- validate alert rule files (`validate-rules`) / config files
  (`validate-config`).

The calling code runs as a **short-lived Juju hook process**: a fresh Python
interpreter is spawned by the Juju agent for each event (e.g. an
`update-status` timer firing roughly every 5 minutes), runs to completion,
and exits. There is no long-lived Python or Go process across invocations.

Within a single hook invocation, the charm may need to transform on the
order of **50,000 individual alert rule expressions** (across many in-memory
rule groups collected from multiple relations/files), each with its own
per-rule Juju topology label set (`juju_model`, `juju_model_uuid`,
`juju_application`, `juju_charm`, and optionally a user-supplied
`juju_unit`).

A single batch always consists of expressions in one query language: the
charm validates/transforms Mimir (PromQL) rules and Loki (LogQL) rules
separately, as two distinct batches, never intermixed. This matches how the
existing CLI already scopes `--format` globally per invocation.

The current CLI exposes one expression (or one file) per process invocation
(`cos-tool --format promql transform -- '<expr>'`,
`cos-tool validate-rules file.yaml`). Using it as-is from Python means
spawning one OS process per expression. At 50,000 expressions per 5-minute
cycle, process-spawn and Go-runtime-init overhead dominates total runtime.

### Options considered

1. **Shared library via cgo + Python FFI (`ctypes`/`cffi`).**
   Rejected. Requires `CGO_ENABLED=1` and a working C toolchain per target
   architecture, breaking the project's existing `CGO_ENABLED=0` static
   cross-compilation story (`.goreleaser.yaml`, CI workflows build for
   `linux/amd64,arm64,ppc64le,s390x,riscv64`). Still pays Go runtime
   init cost on load (just once instead of per call), so the actual
   amortization benefit is no better than a persistent/batched process
   approach. Adds cross-language memory ownership hazards (C string
   lifetimes, Go panics crossing the cgo boundary) and platform-specific
   artifact packaging (`.so`/`.dylib`/`.dll` per arch) for no net gain
   given the usage pattern below.

2. **Persistent HTTP (or Unix-socket) server process.**
   Rejected. A server amortizes startup cost _across_ many calls over the
   _lifetime of the server process_. Here, the Python process is itself
   short-lived and restarts every cycle, so there is no cross-cycle
   lifetime to amortize over — the server would need to be started and
   torn down within the same single hook invocation it's meant to serve,
   which adds a listener, a readiness check, and shutdown handling for no
   benefit over a single bulk subprocess call. It also introduces a new
   local attack surface (port/socket) and operational burden (process
   supervision, port conflicts across concurrent hook executions) that the
   existing one-shot CLI model doesn't have.

3. **Persistent stdio subprocess (`cos-tool serve`, one request per line,
   long-lived pipe).**
   Rejected for this use case, though it would be the right choice if the
   Python side were long-lived. It still requires per-request round-trip
   correlation and doesn't allow trivial internal parallelization of the
   50,000 requests without reinventing id-correlation on top of a
   line-oriented protocol. Since the Python process restarts every cycle
   anyway, there is nothing to amortize a persistent pipe's startup cost
   over.

4. **One-shot batch CLI mode: a single process invocation that reads many
   requests from stdin and writes many results to stdout.**
   Selected. This matches the actual cost driver (process-spawn and
   runtime-init overhead, paid once per 50,000 requests instead of once
   per request) without requiring cgo, a listener, or a persistent
   process. It fits the existing CLI invocation model (spawn, run,
   exit) that the charm hook and `cos-tool`'s build/release pipeline are
   already built around.

## Decision

Add a new `batch` subcommand to `cos-tool` that:

- Reads **newline-delimited JSON (JSONL)** requests from stdin, one per
  line.
- Writes **newline-delimited JSON** results to stdout, one per input line,
  in no particular order.
- Processes requests concurrently (bounded worker pool) for throughput,
  since the existing `tool.Checker` implementations (`PromQL`, `LogQL`) are
  cheap, stateless-per-call, and only carry mutable fields scoped to a
  single call — each unit of work gets its own fresh `PromQL{}`/`LogQL{}`
  instance to avoid data races.
- Reuses the existing **global `--format` flag** (`promql` or `logql`) to
  select the expression language for the whole batch, consistent with
  `transform` and `validate-rules` today. A single batch invocation is
  always single-format; callers needing both Mimir and Loki rules processed
  invoke `cos-tool --format promql batch` and
  `cos-tool --format logql batch` as two separate invocations. No
  per-request `format` field is needed.
- Supports a **per-request `matchers` field** (map of label name → value),
  since Juju topology labels vary per rule (most rules have no explicit
  `juju_unit`, but some do, and this must be respected per-rule rather
  than fixed for the whole batch).
- Round-trips a caller-supplied opaque **`id`** field verbatim in the
  corresponding result, used for correlation. `cos-tool` assigns no
  meaning to `id`; the caller may use a simple incrementing integer.
- Never aborts the batch because of an individual invalid expression,
  malformed input line, or internal panic during processing of a single
  request. Each such failure is reported as `{"id": ..., "ok": false,
"error": "..."}` on its own output line. The process exit code reflects
  only whether the batch _mechanism_ itself succeeded (could read stdin,
  write stdout, and did not crash) — not whether every expression was
  valid.
- Keeps all structured results on stdout; all logging goes to stderr, so
  stdout remains strictly one-JSON-object-per-line and safe to parse
  line-by-line.

### Request/response shape

Invoked as `cos-tool --format promql batch` (or `--format logql batch`).

Request (stdin, one per line):

```json
{
  "id": 0,
  "op": "transform",
  "expr": "rate(foo[5m])",
  "matchers": { "juju_model": "cos" }
}
```

Success response (stdout):

```json
{ "id": 0, "ok": true, "result": "rate(foo{juju_model=\"cos\"}[5m])" }
```

Failure response (stdout) — malformed expression, malformed input line, or
recovered internal panic:

```json
{
  "id": 0,
  "ok": false,
  "error": "1:11: parse error: unexpected left brace '{'"
}
```

### Changes required in the Go codebase

- `cmd/root/root.go`: add a `batch` command. It reuses the existing
  `Before` hook that already resolves the global `--format` flag into a
  `tool.Checker` implementation; batch mode constructs one fresh
  `tool.PromQL{}` / `tool.LogQL{}` instance per request (never a shared
  instance across requests, to avoid data races under concurrent
  processing) and dispatches on the per-request `op` field
  (`transform`, `validate-rules`, `validate-config`).
- Introduce a bounded worker pool (e.g. `errgroup` or a simple channel-based
  pool) to process requests concurrently, writing results to stdout from a
  single goroutine to keep output well-formed (one JSON object per line,
  no interleaved writes).
- Wrap each unit of work in `recover()` so a panic in parser/transform code
  on adversarial input degrades to a single `ok: false` result instead of
  crashing the batch.
- Add JSON encode/decode types for the request/response shapes above.
- No changes required to `pkg/tool` business logic itself — `PromQL` and
  `LogQL` already expose the per-call `Transform`/`ValidateRules`/
  `ValidateConfig` methods needed; batch mode is purely a new entry point
  that calls them more efficiently.
- No changes to build/release tooling — this remains a `CGO_ENABLED=0`
  pure-Go addition, compatible with existing cross-compilation targets in
  `.goreleaser.yaml` and CI.

### Changes required on the Python (charm) side

- A thin batching facade, one per query language (PromQL, LogQL), that
  collects `(expr, matchers)` requests from in-memory rule dicts during a
  single hook invocation, assigns each an incrementing integer `id`, and
  defers actual execution until an explicit flush point.
- On flush: serialize all pending requests for that language to JSONL,
  invoke `cos-tool --format <promql|logql> batch` exactly once via
  `subprocess.run` with the full payload on stdin, parse the JSONL result
  stream, and apply results back to the originating in-memory rule objects
  by `id`. If both Mimir and Loki rules need processing in the same hook
  invocation, this results in exactly two `cos-tool` invocations total
  (one per format), not one per expression.
- Treat a non-zero exit code as "the batch mechanism failed" (fatal);
  treat `ok: false` on an individual result as "this expression was
  invalid" (non-fatal, logged with file/rule context that only Python
  has).

## Consequences

- Per-hook-invocation subprocess count drops from ~50,000 to 1, eliminating
  the dominant source of overhead (process spawn and Go runtime init).
- `cos-tool`'s existing single-expression `transform` and file-oriented
  `validate-rules`/`validate-config` commands are unchanged and remain
  available for ad hoc/manual use.
- The batch protocol is intentionally agnostic to file layout, rule names,
  or YAML structure; all such bookkeeping stays in Python, which already
  needs it to extract expressions from rule files in the first place.
- Future growth beyond ~50,000 requests per cycle, should it occur, can be
  addressed by chunking flushes or streaming stdin/stdout incrementally,
  without changing the protocol shape decided here.
