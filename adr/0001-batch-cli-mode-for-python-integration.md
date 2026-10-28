# 1. Batch CLI mode for high-volume Python integration

## Status

Accepted

## Context

`cos-tool` is invoked by Juju charm code (Python) as a short-lived
subprocess per call: once per alert rule expression to inject Juju
topology label matchers (`transform`), or once per rule/config file to
validate (`validate-rules`, `validate-config`). The calling Python process
itself is also short-lived (a Juju hook, e.g. an `update-status` timer
firing every ~5 minutes) — there is no long-lived process on either side
across invocations.

A single hook invocation may need to transform on the order of **50,000
alert rule expressions**, each with its own per-rule Juju topology label
set. Calling `cos-tool` once per expression means 50,000 process spawns
per cycle, and process-spawn/Go-runtime-init overhead dominates total
runtime at that volume.

A batch is always single-language: callers process Mimir (PromQL) and
Loki (LogQL) rules as separate batches, never intermixed, matching the
existing global `--format` flag.

### Alternatives rejected

- **cgo shared library + Python FFI.** Requires `CGO_ENABLED=1` and a C
  toolchain per target, breaking the project's `CGO_ENABLED=0` static
  cross-compilation (`.goreleaser.yaml`, CI). Still pays Go runtime init
  once per load rather than once per call — no better amortization than a
  batched process, with added memory-ownership and panic-across-cgo-boundary
  hazards for no net gain.
- **Persistent HTTP/socket server.** A server amortizes startup cost across
  the _server's_ lifetime. Since the Python side restarts every cycle,
  there's no cross-cycle lifetime to amortize over — the server would be
  started and torn down within the same invocation it serves, adding a
  listener, readiness check, and shutdown handling for no benefit over one
  bulk subprocess call.
- **Persistent stdio subprocess (`serve` mode).** Same amortization problem
  as above given a short-lived caller, plus it requires per-request
  round-trip correlation and complicates internal parallelization versus a
  single bulk read/process/write pass.

**Selected:** a one-shot `batch` subcommand that reads many requests from
stdin and writes many results to stdout in a single process invocation —
reducing spawn count from O(expressions) to O(1) per cycle without cgo, a
listener, or a persistent process.

## Decision

Add a `batch` subcommand that:

- Reads **newline-delimited JSON (JSONL)** requests from stdin, one per
  line; writes JSONL results to stdout, one per input line, in no
  particular order.
- Processes requests concurrently (bounded worker pool), constructing a
  fresh `tool.PromQL{}` / `tool.LogQL{}` per request — never shared across
  requests, since `Transform` mutates fields on the receiver.
- Uses the existing global `--format` flag for the whole batch (no
  per-request format field): `cos-tool --format promql batch` /
  `cos-tool --format logql batch`.
- Accepts a per-request `matchers` field (map of label name → value), since
  topology labels vary per rule.
- Round-trips a caller-supplied opaque `id` field verbatim for correlation;
  `cos-tool` assigns it no meaning.
- Never aborts the batch due to one invalid expression, malformed input
  line, or a panic while processing a single request — each becomes
  `{"id": ..., "ok": false, "error": "..."}` on its own output line.
  Process exit code reflects only whether the batch mechanism itself
  succeeded (stdin read, stdout write, no crash), not whether individual
  expressions were valid.
- Keeps stdout strictly one-JSON-object-per-line; all logging goes to
  stderr.

### Request/response shape

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
recovered panic:

```json
{
  "id": 0,
  "ok": false,
  "error": "1:11: parse error: unexpected left brace '{'"
}
```

### Implementation notes

- `cmd/root/root.go`: add a `batch` command reusing the existing `Before`
  hook (resolves global `--format` into a `tool.Checker` once); dispatch
  per-request on `op` (`transform`, `validate-rules`, `validate-config`).
- Bounded worker pool (e.g. `errgroup` or channel-based) for concurrent
  processing, with results written to stdout from a single goroutine to
  avoid interleaved/malformed output lines.
- Wrap each unit of work in `recover()` so a panic on adversarial input
  degrades to one `ok: false` result rather than crashing the batch.
- New JSON request/response types for the shapes above.
- No changes needed to `pkg/tool` — `PromQL`/`LogQL` already expose the
  per-call `Transform`/`ValidateRules`/`ValidateConfig` methods required;
  `batch` is purely a new entry point.
- No changes to build/release tooling — remains `CGO_ENABLED=0`, compatible
  with existing cross-compilation targets.

## Consequences

- Per-cycle subprocess count drops from ~50,000 to 1 (or 2, if a cycle
  needs both PromQL and LogQL batches).
- Existing single-expression `transform` and file-oriented
  `validate-rules`/`validate-config` commands are unchanged.
- The protocol is agnostic to file layout, rule names, or YAML structure —
  callers correlate results back to their own data structures via `id`.
- Should request volume per cycle grow substantially, this can be
  addressed by chunked or streamed stdin/stdout handling without changing
  the protocol shape.
