# 1. Batch CLI mode for high-volume Python integration

## Status

Accepted

## Context

`cos-tool` is invoked by Juju charm code (Python) as a short-lived
subprocess per call: once per alert rule expression, to inject Juju
topology label matchers (`transform`). The calling Python process itself
is also short-lived (a Juju hook, firing on a timer every few minutes) —
there is no long-lived process on either side across invocations.

A single hook invocation may need to transform on the order of **50,000
alert rule expressions**, each with its own per-rule topology labels.

Measurement shows per-invocation process-spawn/runtime-init overhead is
approximately **20ms**. The payload of a single call is small (one PromQL
or LogQL expression and a handful of label matchers), so the actual
string/AST work per expression is assumed to be small relative to that
20ms. That assumption is deliberately left unquantified: it has not been
measured, and no per-expression work figure is claimed here. What _is_
measured is that at 50,000 expressions per cycle, spawn overhead alone
accounts for roughly **1,000 seconds** of wall time, which is reason
enough to eliminate it.

Accordingly, this ADR does not set a throughput target. Its goal is
narrow and verifiable: remove the per-expression process spawn. Absolute
throughput of the resulting batch mode should be measured once
implemented, and a target set then — informed by real numbers rather
than estimates.

> **Settled after implementation.** Both figures were measured and the
> target set: 50,000 expressions in **under 30 seconds** end to end,
> against **~2.5-5.5s** measured. The ~20ms spawn estimate above was
> confirmed at ~10-17ms per invocation. See
> [Measured throughput](../design/0001-batch-transform-interfaces.md#measured-throughput)
> for the numbers and `make bench` to reproduce them.

### Scope

In scope: the per-expression label-injection operation (`transform`),
which is invoked at a volume proportional to the number of alert rules
and is therefore subject to the overhead described above.

Out of scope:

- Rule-file validation (`validate-rules`) already accepts many files in
  one invocation, so it does not incur a per-item spawn.
- Config-file validation (`validate-config`) is invoked at most a small,
  fixed number of times per cycle (proportional to the number of config
  files, not the number of rules).
- Caching/memoization of repeated expressions. Where the same expression
  recurs across many rules, deduplicating it is the caller's
  responsibility; it composes with batch mode rather than competing with
  it, and `cos-tool` holds no cross-invocation state to cache in.

### Requirements

- Reduce process-spawn count from one-per-expression to a small constant
  number per hook invocation, without materially increasing deployment or
  build complexity.
- Support per-expression topology label sets, since these vary rule to
  rule within the same invocation.
- Support correlating each result back to the request that produced it,
  so a caller managing many in-memory rule objects can apply results
  without relying on output ordering.
- Keep caller-side and tool-side memory bounded, and make it impossible
  for a caller to deadlock against the tool by mismanaging I/O.
- An individual expression that is semantically invalid (fails to parse)
  must not abort processing of the rest of the batch, and must be
  distinguishable from a successful result.
- An individual request that is structurally malformed (cannot be
  interpreted as a request at all — for example, missing a required
  field) must likewise not abort the batch. It is reported as its own
  distinguishable per-item outcome, so one bad line cannot cost the
  caller the other 49,999 results.
- A batch, once the invocation exits successfully, must produce exactly
  one result for every request submitted — partial or silently truncated
  output must not occur. If the invocation cannot complete the batch
  (unreadable input, unwritable output, unrecoverable internal error),
  this must be reflected unambiguously in the invocation's overall
  outcome, rather than left to be inferred from a short or missing
  output.
- The record format must carry expression content and label values
  intact, including embedded newlines and other control characters,
  without corrupting the framing between one item and the next. (Content
  is assumed to be valid UTF-8; see Consequences.)
- The interface must be versioned and extensible: callers must be able to
  detect whether the binary in front of them supports batch mode at all,
  and new fields must be addable later without requiring a lock-step
  upgrade of every caller.
- Preserve the existing single-expression behavior for ad hoc/manual use;
  this is an additive capability.

### Alternatives rejected

- **Shared library + FFI from Python.** Rejected primarily on build and
  deployment complexity grounds: it would require enabling cgo and a C
  toolchain per target platform, which conflicts with the project's
  current static, cgo-free cross-compilation story (`CGO_ENABLED=0`
  across five architectures). This cost was judged not worth taking on
  for a problem that a plain batched invocation already solves.
- **Persistent server (HTTP or socket), long-lived across hook
  invocations.** A server amortizes startup cost across its own lifetime.
  Since the calling process restarts every cycle, there is no cross-cycle
  lifetime to amortize over — the server would need to be started and
  stopped within the single invocation it serves, adding listener
  lifecycle and readiness/shutdown handling for no benefit over a single
  bulk call.
- **Persistent subprocess serving requests one at a time over a
  long-lived connection.** Suffers the same lack of cross-cycle
  amortization as above, and additionally requires per-request
  correlation and complicates concurrent processing compared to handling
  one bulk payload at once.
- **Streaming the batch over stdin and results over stdout.** Attractive
  on paper, but it makes a deadlock reachable by an ordinary caller: if
  Python writes all 50,000 request lines before reading any results, both
  processes block once the ~64KB pipe buffers fill — the writer waiting
  for the reader to drain stdout, the reader waiting for stdin to be
  consumed. Avoiding it requires the caller to read and write
  concurrently (threads or a selector loop), which is exactly the kind of
  subtle obligation that is easy to get wrong in charm code and hard to
  diagnose when it hangs a hook. Files remove the failure mode entirely,
  and leave the batch on disk for inspection when something does go
  wrong.
- **Repeated flags or positional arguments on the existing command**
  (`transform --expr ... --expr ...`). The cheapest possible change, but
  it does not scale to the required volume: the total argument vector is
  capped (`ARG_MAX`, commonly ~2MB, with per-argument limits as well), so
  tens of thousands of expressions plus their per-rule label sets would
  overflow it — and would do so as an opaque `E2BIG` at `exec` time,
  dependent on payload size, rather than as a clear error.
- **Avoiding the 50,000 transforms altogether** by injecting topology at
  a coarser granularity (per rule group, or downstream via
  `external_labels`/relabeling). Rejected because the matchers genuinely
  vary per rule: topology is resolved from the originating application
  and unit, so there is no coarser unit of work that shares one matcher
  set. The volume is inherent to the problem, not an artifact of the
  interface.

**Selected:** process many items in a single process invocation, reading
all requests from a batch file and writing all results to a results
file, reducing spawn count from one-per-item to one per batch.

## Decision

Add a batch mode to the existing `transform` command that accepts many
label-injection requests in one invocation and produces one result per
request:

```bash
cos-tool [--format promql|logql] transform \
    --batch requests.jsonl \
    --out results.jsonl \
    [--label-matcher juju_model=cos ...]
```

### Transport

- `--batch <path>` names the **batch file** the caller has written;
  `--out <path>` names the **results file** the tool writes. Neither side
  streams through a pipe, so the stdin/stdout deadlock described above is
  not reachable, and both sides can process the files line by line with
  bounded memory.
- Both flags are required together, and are mutually exclusive with the
  positional `<expression>` argument. `--batch` without `--out` (or vice
  versa), or `--batch` alongside a positional expression, is a usage
  error that fails the invocation immediately — before any work is done
  and before any file is created. This keeps batch mode an additive flag
  pair on a command callers already use, rather than a second subcommand
  duplicating `--format` and `--label-matcher`.
- `stdout`/`stderr` carry only diagnostics, never result data. A caller
  that wants to log tool output can do so without having to separate
  diagnostics from results.

### Format

- Both files are **JSONL** (newline-delimited JSON): exactly one JSON
  object per line, one request per line in, one result per line out. This
  gives line-level framing that tolerates embedded newlines and control
  characters inside string values (JSON escapes them), per-line
  independence so a single bad line is recoverable, and an object model
  in which unrecognized fields can be safely ignored for forward
  compatibility.
- A request object carries:
  - `id` — required; a caller-supplied opaque identifier, returned
    unchanged alongside its result, so callers can match results back to
    their own data.
  - `expr` — required; the expression to transform.
  - `matchers` — optional; the topology labels specific to _this_
    expression. The invocation-level `--label-matcher` set acts as a
    **batch-wide default that records extend**: the effective matcher set
    for a request is the `--label-matcher` set merged per key with the
    record's `matchers`, with the record winning on conflict. This lets
    callers factor out the labels that are constant for the whole batch
    (typically `juju_model` and `juju_model_uuid`) and repeat only what
    actually varies per rule, which is a material size reduction on a
    50,000-line batch file.
  - `format` — optional; `promql` or `logql`. Defaults to the
    invocation-level `--format`, which remains the common case: callers
    keep Prometheus and Loki rule handling separate (different charm
    libraries, different relation events), so a batch is normally
    single-language. The per-record field exists so that mixing is
    possible without a new interface.
  - `version` — optional; the interface version the request is written
    against. Defaults to the earliest version.
- A result object carries `version`, the request's `id`, and a `status`:
  - `ok` — succeeded; the transformed expression is in `expr`.
  - `error` — the expression is semantically invalid (failed to parse);
    `error` carries the message.
  - `malformed` — the line could not be interpreted as a request (not
    valid JSON, missing a required field, wrong types). `line` carries
    the 1-based input line number so the caller can locate it even when
    `id` could not be recovered; `id` is included when it could.
  - `unsupported_version` — the request is well-formed but declares an
    interface version this binary does not implement.

Given batch-wide defaults on the command line:

```bash
cos-tool transform --batch requests.jsonl --out results.jsonl \
    --label-matcher juju_model=cos \
    --label-matcher juju_model_uuid=12345
```

example request lines carry only what varies per rule:

```jsonl
{"id":"rule-42","expr":"rate(http_requests_total{job=\"myjob\"}[5m]) > 0.5","matchers":{"juju_application":"proxy","juju_unit":"proxy/1"}}
{"id":"rule-43","expr":"sum(rate({job=\"nginx\"}[5m]))","matchers":{"juju_application":"nginx"},"format":"logql"}
{"id":"rule-44","expr":"up == 0"}
```

Example result lines:

```jsonl
{"version":1,"id":"rule-42","status":"ok","expr":"rate(http_requests_total{job=\"myjob\",juju_application=\"proxy\",juju_model=\"cos\",juju_model_uuid=\"12345\",juju_unit=\"proxy/1\"}[5m]) > 0.5"}
{"version":1,"id":"rule-43","status":"ok","expr":"sum(rate({job=\"nginx\", juju_application=\"nginx\", juju_model=\"cos\", juju_model_uuid=\"12345\"}[5m]))"}
{"version":1,"id":"rule-44","status":"ok","expr":"up{juju_model=\"cos\",juju_model_uuid=\"12345\"} == 0"}
```

`rule-44`, which carries no `matchers` of its own, is transformed with
just the two batch-wide defaults. A line that could not be read as a
request at all would instead produce a result such as:

```jsonl
{
  "version": 1,
  "line": 7,
  "status": "malformed",
  "error": "missing required field \"id\""
}
```

### Semantics

- Results are written in **input order**. Correlation does not depend on
  it — that is what `id` is for — but deterministic output is nearly free
  here and makes results diffable, testable, and comparable between runs.
- No per-item timeout. Transformation is pure, CPU-bound, side-effect
  free work over a small payload, using RE2 regexes (linear time, no
  catastrophic backtracking) and a bounded parser; there is no observed
  class of input that hangs. A wall-clock bound would also be
  uncancellable in practice (the transform takes no `context.Context`, so
  "timing out" would mean abandoning a goroutine that keeps burning CPU)
  and would make results load-dependent: the same rule could succeed on
  an idle machine and fail on a busy one, causing alert rules to flap
  across reconciliations. If pathological inputs are ever found, a
  deterministic bound (e.g. on expression size or AST node count) is the
  right tool, not a timer.
- The invocation's **exit status** reflects only whether the tool itself
  completed: it could read the batch file, write the results file, and
  hit no unrecoverable internal error. It does not reflect whether every
  individual expression was valid — per-item failures are data in the
  results file. A zero exit status guarantees exactly one result line per
  request line. A non-zero exit status means the results file must not be
  treated as complete, even if some lines were written before the
  failure.
- `--label-matcher` is **optional** in batch mode, and so is the
  per-record `matchers` field. When neither supplies a matcher for a
  request, the effective matcher set is empty and the transform is a
  parse-and-normalize pass: the expression is still validated and
  re-emitted in `cos-tool`'s canonical form, just with nothing injected.
  This is a legitimate use (bulk expression validation) rather than an
  error, so it is not rejected.
- Because merging is per key and records win on conflict, a record can
  **override** an inherited default but cannot **remove** one: there is no
  way in version 1 to say "apply every batch-wide matcher except this
  one." A caller needing that should pass only the matchers that are
  genuinely universal on the command line and carry the rest per record.
  If a real need for removal appears, it is an additive change (e.g. a
  JSON `null` value meaning "unset") that the versioned record format can
  absorb without breaking existing callers.
- An **empty batch file** is valid: the tool writes an empty results file
  and exits zero.

### Versioning and discovery

- The batch interface carries an integer version, starting at 1. Every
  result line reports the `version` that produced it, so a results file
  is self-describing.
- `cos-tool transform --batch-version` prints the highest interface
  version the binary implements and exits zero, touching no files. A
  binary without batch mode rejects the unknown flag and exits non-zero,
  so this doubles as a cheap, side-effect-free capability probe callers
  can run before going to the trouble of writing a batch file.
- Additive changes (new optional fields) require no caller changes:
  unrecognized fields are ignored on both sides. A request that declares
  a `version` the binary does not implement is reported per-item as
  `unsupported_version`, so a caller that has drifted ahead of the
  deployed binary gets a precise, actionable signal rather than a
  wholesale failure.

## Consequences

- Per-cycle process-spawn count for `transform` drops from on the order
  of tens of thousands to a small constant. Against the measured ~20ms
  per-invocation overhead, this removes roughly 1,000 seconds of pure
  overhead from a 50,000-expression cycle.
- Absolute throughput of batch mode is **not** predicted by this ADR,
  because per-expression work was never measured. It must be benchmarked
  once implemented, and a throughput target set from those numbers. If
  per-expression work turns out to dominate at this volume, further work
  (concurrency within the batch, caller-side deduplication, or cheaper
  transformation) is a separate decision and a separate ADR.
  **Resolved:** per-expression work does not dominate — it is roughly two
  orders of magnitude smaller than the spawn cost it replaces — so none of
  that further work is needed. The target is recorded in the design
  document rather than here, so that it can be revised against new
  measurements without reopening an accepted decision.
- `transform` gains `--batch`, `--out`, and `--batch-version`;
  `validate-rules` and `validate-config` are unchanged and continue to be
  invoked as today. Existing single-expression invocation behavior —
  `transform [--label-matcher ...] -- <expression>` printing the result to
  stdout — is unaffected and remains available, since batch mode is
  reached only by passing `--batch`.
- Callers must check the invocation's exit status before trusting the
  results file, and should treat a non-zero exit status as "no usable
  result," not as "a partial result."
- `--out` is written unconditionally and will overwrite an existing file
  at that path. Callers are responsible for choosing a path they own;
  a per-invocation temporary directory is the obvious choice.
- Callers must inspect `status` per result. A zero exit status means
  every request was answered — not that every answer was a success.
- Semantic expression errors move from `stderr` (where the
  single-expression path reports them) to the results file as data.
  Anything that greps `stderr` for transformation failures will not see
  batch-mode failures.
- Both sides exchange files, so the caller needs a writable directory for
  the batch and results files and is responsible for cleaning them up.
  In exchange, a failed run leaves both files on disk for inspection,
  which is a significant debugging improvement over a piped stream.
- Memory stays bounded on both sides: records are independent lines, so
  neither the caller nor the tool needs to hold the whole batch in
  memory. Note that JSONL still requires each individual record to fit in
  one line, and readers must be configured with a line limit generous
  enough for the longest expression plus its matchers.
- JSON string values must be valid UTF-8. Go's encoder silently replaces
  invalid UTF-8 with U+FFFD, so the "content carried intact" guarantee is
  **valid UTF-8 carried intact**. This is not expected to matter for
  PromQL/LogQL expressions and label values, but it is a real limit of
  the chosen format rather than an unconditional guarantee.
- `id` is treated as opaque and is not checked for uniqueness. A caller
  that submits duplicate `id`s will get one result per request as
  promised, but will lose data if it reassembles results into a
  dictionary keyed by `id`. Generating unique ids is the caller's
  responsibility.
- The batch interface has no knowledge of file layout, rule naming, or
  any other structure external to a single expression plus its matchers;
  callers remain responsible for mapping results back to whatever
  structure they maintain, and for deduplicating repeated expressions if
  they choose to.
- If request volume per cycle grows substantially beyond current
  expectations, the line-delimited format already permits incremental
  handling on both sides without changing the overall approach.
