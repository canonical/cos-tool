# 1. Batch CLI mode for high-volume Python integration

## Status

Accepted

## Context

`cos-tool` is invoked by Juju charm code (Python) as a short-lived
subprocess per call: once per alert rule expression to inject Juju
topology label matchers (`transform`), or once per rule/config file to
validate. The calling Python process itself is also short-lived (a Juju
hook, firing on a timer every few minutes) — there is no long-lived
process on either side across invocations.

A single hook invocation may need to transform on the order of **50,000
alert rule expressions**, each with its own per-rule topology labels.
Calling `cos-tool` once per expression means tens of thousands of process
spawns per cycle, and process-spawn/runtime-init overhead dominates total
runtime at that volume.

A batch always consists of expressions in a single query language:
callers process Mimir (PromQL) and Loki (LogQL) rules as separate batches,
never intermixed.

### Requirements

- Reduce process-spawn count from one-per-expression to a small constant
  number per hook invocation, without materially increasing deployment or
  build complexity.
- Support per-expression topology label sets, since these vary rule to
  rule within the same invocation.
- Support correlating each result back to the expression that produced it,
  so a caller managing many in-memory rule objects can apply results
  without relying on output ordering.
- An individual invalid or malformed expression must not abort processing
  of the rest of the batch, and must be distinguishable from a successful
  result.
- Preserve the existing single-expression and file-validation behavior for
  ad hoc/manual use; this is an additive capability.

### Alternatives rejected

- **Shared library + FFI from Python.** Would require enabling cgo and a
  C toolchain per target platform, which conflicts with the project's
  current static, cgo-free cross-compilation story (`CGO_ENABLED=0`). It also doesn't avoid
  the main cost driver: runtime initialization still happens once per
  load rather than being eliminated, offering no meaningful amortization
  advantage over a batched-process approach, while introducing
  cross-language memory-ownership and failure-propagation hazards.
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

**Selected:** process many expressions in a single process invocation,
reading all requests and producing all results in one pass, reducing
spawn count from one-per-expression to one per batch (per query
language).

## Decision

Add a batch mode that accepts many expression-transform requests in one
invocation and returns one result per request:

- Input and output are line-delimited, machine-readable records (one
  request per line in, one result per line out), so arbitrarily many
  expressions can be streamed through a single process invocation without
  needing an upfront count or in-memory assembly of one giant document.
- The query language (PromQL vs. LogQL) is selected once for the whole
  invocation, consistent with how the tool already selects behavior
  today; a batch is not mixed-language.
- Each request carries its own topology label set, independent of other
  requests in the same batch.
- Each request carries a caller-supplied opaque identifier that is
  returned unchanged alongside its result, so callers can match results
  back to their own data without depending on output order.
- Each result indicates success or failure explicitly; a failure (invalid
  expression, malformed request, or any other per-item error) is reported
  as data on that item's result, not as a fatal error for the whole
  invocation.
- The invocation's overall exit status reflects only whether batch
  processing completed at all (e.g. could read input, could write output);
  it does not reflect whether every individual expression was valid.
- Only result data is written to the output stream; diagnostic logging is
  kept separate, so output remains strictly machine-parseable line by
  line.

## Consequences

- Per-cycle process-spawn count drops from on the order of tens of
  thousands to a small constant (one per query language processed that
  cycle).
- Existing single-expression and file-based validation behavior is
  unaffected and remains available.
- The batch interface has no knowledge of file layout, rule naming, or
  any other structure external to a single expression plus its labels;
  callers remain responsible for mapping results back to whatever
  structure they maintain.
- If request volume per cycle grows substantially beyond current
  expectations, the line-delimited design allows incremental/streamed
  handling without changing the overall approach.
