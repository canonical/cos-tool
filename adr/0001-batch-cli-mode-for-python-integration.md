# 1. Batch CLI mode for high-volume Python integration

## Status

Accepted

## Context

`cos-tool` is invoked by Juju charm code (Python) as a short-lived
subprocess per call: once per alert rule expression to inject Juju
topology label matchers (`transform`), or once per rule file to validate.
The calling Python process itself is also short-lived (a Juju hook,
firing on a timer every few minutes) — there is no long-lived process on
either side across invocations.

A single hook invocation may need to transform on the order of **50,000
alert rule expressions**, each with its own per-rule topology labels.
Measurement shows per-invocation process-spawn/runtime-init overhead is
approximately **20ms**, roughly **10x** the actual string-manipulation
work being done per expression (approximately 2ms). At 50,000 expressions
per cycle, this overhead — not the actual transformation logic — dominates
total runtime.

A batch always consists of expressions or rule files in a single query
language: Prometheus (PromQL) and Loki (LogQL) rules are already produced
and processed by two separate charm libraries, triggered by two distinct,
independent relation events, so they are never combined into one caller-side
operation to begin with.

### Scope

In scope: the per-expression label-injection operation (`transform`), and
the per-file rule validation operation (`validate-rules`), since both are
invoked at a volume proportional to the number of alert rules and are
therefore both subject to the overhead described above.

Out of scope: config-file validation (`validate-config`). It is invoked at
most a small, fixed number of times per cycle (proportional to the number
of config files, not the number of rules), so it is not a contributor to
the overhead this ADR addresses.

### Requirements

- Reduce process-spawn count from one-per-expression/one-per-file to a
  small constant number per hook invocation, without materially increasing
  deployment or build complexity.
- Meet a concrete throughput target: process 50,000 expressions in under
  10 seconds.
- Support per-expression topology label sets, since these vary rule to
  rule within the same invocation.
- Support correlating each result back to the expression or file that
  produced it, so a caller managing many in-memory rule objects can apply
  results without relying on output ordering.
- An individual item that is semantically invalid (an expression that
  fails to parse, or a rule file that fails validation) must not abort
  processing of the rest of the batch, and must be distinguishable from a
  successful result.
- An individual item whose processing does not complete within a bounded
  time must not be allowed to stall the rest of the batch indefinitely; it
  must instead resolve to an explicit, distinguishable failure outcome for
  that item.
- A well-formed batch, once it exits successfully, must produce exactly
  one result for every item submitted — partial or silently truncated
  output must not occur. If the invocation cannot complete the batch (for
  any reason, including an item exceeding its time bound so many times as
  to be treated as an aggregate failure, or an unrecoverable internal
  error), this must be reflected unambiguously in the invocation's overall
  outcome, rather than left to be inferred from a short or missing output.
- A batch item that is not merely semantically invalid but structurally
  malformed (cannot be interpreted as a request at all — for example, it
  is missing information required to process or even identify it) is
  treated as invalid input to the tool itself, not as a per-item failure;
  this fails the invocation as a whole rather than being reported as one
  result among many.
- The chosen record format must be able to carry arbitrary expression or
  file content verbatim — including embedded newlines and other control
  characters — without corrupting the framing between one item and the
  next.
- The interface should be extensible without breaking existing callers:
  it must be possible to add new information to requests or results later
  without requiring a lock-step upgrade of every caller.
- Preserve the existing single-expression and file-validation behavior for
  ad hoc/manual use; this is an additive capability.

### Alternatives rejected

- **Shared library + FFI from Python.** Rejected primarily on build and
  deployment complexity grounds: it would require enabling cgo and a C
  toolchain per target platform, which conflicts with the project's
  current static, cgo-free cross-compilation story. This cost was judged
  not worth taking on for a problem that a plain batched invocation already
  solves.
- **Persistent server (HTTP or socket), long-lived across hook
  invocations.** A server amortizes startup cost across its own lifetime.
  Since the calling process restarts every cycle, there is no cross-cycle
  lifetime to amortize over — the server would need to be started and
  stopped within the single invocation it serves, adding listener
  lifecycle and readiness/shutdown handling for no benefit over a single
  bulk call.
- **Persistent subprocess serving requests one at a time over a long-lived
  connection.** Suffers the same lack of cross-cycle amortization as
  above, and additionally requires per-request correlation and complicates
  concurrent processing compared to handling one bulk payload at once.

**Selected:** process many items in a single process invocation, reading
all requests and producing all results in one pass, reducing spawn count
from one-per-item to one per batch (per query language).

## Decision

Add a batch mode that accepts many requests — each either a label-injection
request for a single expression, or a validation request for a single rule
file's contents — in one invocation, and returns one result per request:

- Input and output are line-delimited, machine-readable records (one
  request per line in, one result per line out), so arbitrarily many
  items can be streamed through a single process invocation without
  needing an upfront count or in-memory assembly of one giant document.
- The query language (PromQL vs. LogQL) is selected once for the whole
  invocation, consistent with how the tool already selects behavior
  today. This is a deliberate simplification rather than a limitation in
  practice: callers already keep Prometheus and Loki rule handling
  entirely separate (different charm libraries, different relation
  events), so there is no caller-side scenario where mixing languages in
  one batch would save an invocation that isn't already saved by this
  design.
- Each expression-transform request carries its own topology label set,
  independent of other requests in the same batch.
- Each request carries a caller-supplied opaque identifier that is
  returned unchanged alongside its result, so callers can match results
  back to their own data without depending on output order.
- Each result indicates success or failure explicitly; a semantic failure
  (invalid expression, failed rule validation) or a timeout is reported as
  data on that item's result, not as a fatal error for the whole
  invocation. Processing of each item is bounded by a timeout so that one
  item cannot stall the rest of the batch.
- A request that cannot be interpreted at all (structurally malformed,
  missing required information) is treated as invalid input to the tool
  itself: the invocation fails as a whole rather than emitting a
  best-effort result for it.
- The invocation's overall exit status reflects only whether the tool
  itself completed successfully (could read input, could write output,
  encountered no unrecoverable internal error, and every request was at
  least well-formed) — not whether every individual expression or rule
  file was valid. A non-zero exit status means the output must not be
  treated as complete, even if some result lines were produced before the
  failure; a zero exit status guarantees exactly one result per request.
- Only result data is written to the output stream; diagnostic logging is
  kept separate, so output remains strictly machine-parseable line by
  line.
- The record format is chosen to tolerate future additions (e.g.
  unrecognized fields can be safely ignored) so that new capabilities can
  be introduced without breaking callers built against an earlier version
  of the interface.

## Consequences

- Per-cycle process-spawn count drops from on the order of tens of
  thousands to a small constant (one per query language processed that
  cycle) — this is what the design enables, though the actual count in
  practice also depends on how the caller chooses to batch its work.
- Given measured per-invocation overhead (~20ms) versus per-expression
  work (~2ms), eliminating per-expression spawns is expected to bring
  50,000-expression batches well under the 10-second target; this should
  be confirmed against the target once implemented.
- `validate-rules` is included in batch mode; `validate-config` is not,
  and continues to be invoked as today.
- Existing single-expression and single-file invocation behavior is
  unaffected and remains available.
- The batch interface has no knowledge of file layout, rule naming, or
  any other structure external to a single expression/file plus its
  inputs; callers remain responsible for mapping results back to whatever
  structure they maintain.
- Callers must check the invocation's overall exit status before trusting
  any output, and should treat a non-zero exit status as "no usable
  result," not as "a partial result."
- If request volume per cycle grows substantially beyond current
  expectations, the line-delimited design allows incremental/streamed
  handling without changing the overall approach.
