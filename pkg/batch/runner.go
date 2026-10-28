package batch

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Options carries the invocation-level defaults a batch runs under.
type Options struct {
	// DefaultFormat is the format for records that do not name one, from
	// --format.
	DefaultFormat string

	// DefaultMatchers are the batch-wide matchers that records extend, from
	// --label-matcher. Records win per key on conflict.
	DefaultMatchers map[string]string

	// Diagnostics receives notices about individual records that do not
	// belong in the results file, currently only recovered panics. Result data
	// is never written here. Nil discards them.
	Diagnostics io.Writer
}

// Runner processes a batch of requests into a batch of results.
//
// It owns the central invariant: on success, exactly one result line is
// written for every input line, in input order. Every branch of record
// handling therefore terminates in a result.
type Runner struct {
	opts     Options
	resolver Resolver
}

// NewRunner returns a Runner that transforms records using res, under opts.
func NewRunner(opts Options, res Resolver) *Runner {
	return &Runner{opts: opts, resolver: res}
}

// Run processes every line of in and writes one result line to out per input
// line, in input order.
//
// Run returns a non-nil error only for faults that mean the batch did not
// complete: unreadable input, unwritable or unflushable output. Per-record
// outcomes are result lines, never error returns, so a caller must inspect
// each result's status even when Run succeeds.
//
// An empty input is valid and produces an empty output.
func (r *Runner) Run(in io.Reader, out io.Writer) error {
	dec := NewDecoder(in)
	enc := NewEncoder(out)

	for {
		rec, ok := dec.Next()
		if !ok {
			break
		}

		if err := enc.Encode(r.handle(rec)); err != nil {
			return fmt.Errorf("writing result for line %d: %w", rec.Line, err)
		}
	}

	if err := dec.Err(); err != nil {
		return fmt.Errorf("reading batch: %w", err)
	}

	// Explicit, not deferred: a flush failure means the results file is short,
	// which must surface as a failed invocation rather than a quiet success.
	if err := enc.Flush(); err != nil {
		return fmt.Errorf("writing results: %w", err)
	}

	return nil
}

// handle produces the single result for one record.
func (r *Runner) handle(rec Record) Result {
	if rec.Fault != nil {
		return r.malformed(rec.Line, rec.RecoveredID, rec.Fault)
	}

	req := rec.Req

	// Version is checked before v1's required-field rules on purpose: a future
	// record that omits a field v1 requires should be reported as an
	// unsupported version, which is actionable, rather than as malformed,
	// which would point at the wrong problem.
	if req.Version != nil && *req.Version != Version {
		return Result{
			Version: Version,
			ID:      req.ID,
			Line:    rec.Line,
			Status:  StatusUnsupportedVersion,
			Error: fmt.Sprintf("request declares batch interface version %d; this binary implements version %d",
				*req.Version, Version),
		}
	}

	if req.ID == nil {
		return r.malformed(rec.Line, nil, fmt.Errorf("missing required field %q", "id"))
	}
	if req.Expr == nil {
		return r.malformed(rec.Line, req.ID, fmt.Errorf("missing required field %q", "expr"))
	}

	format := r.opts.DefaultFormat
	if req.Format != nil {
		format = *req.Format
	}

	transformer, ok := r.resolver.Transformer(format)
	if !ok {
		return r.malformed(rec.Line, req.ID, fmt.Errorf("unknown format %q; expected one of %s",
			format, r.knownFormats()))
	}

	expr, err := r.transform(transformer, *req.Expr, Merge(r.opts.DefaultMatchers, req.Matchers))
	if err != nil {
		return Result{
			Version: Version,
			ID:      req.ID,
			Line:    rec.Line,
			Status:  StatusError,
			Error:   err.Error(),
		}
	}

	return Result{
		Version: Version,
		ID:      req.ID,
		Status:  StatusOK,
		Expr:    expr,
	}
}

// transform calls the transformer, converting a panic into an ordinary error.
//
// The parsers already contain their own panics, but cos-tool's template
// variable handling does not, and an uncontained panic would cost the caller
// every other result in the batch. Surviving the batch is the stronger
// guarantee, so a panic is reported as a failure of that one expression and
// announced on Diagnostics, where it stays visible as the bug it is.
func (r *Runner) transform(t Transformer, expr string, matchers map[string]string) (result string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("internal error transforming expression: %v", recovered)
			r.diagnose("cos-tool: %v\n", err)
		}
	}()

	// The Transform signature takes a pointer to a map; contain that here
	// rather than propagate it through this package.
	return t.Transform(expr, &matchers)
}

// malformed builds a malformed result, which always carries the input line
// number because the id may not be recoverable.
func (r *Runner) malformed(line int, id *string, cause error) Result {
	return Result{
		Version: Version,
		ID:      id,
		Line:    line,
		Status:  StatusMalformed,
		Error:   cause.Error(),
	}
}

func (r *Runner) knownFormats() string {
	lister, ok := r.resolver.(interface{ Formats() []string })
	if !ok {
		return "the supported formats"
	}

	formats := lister.Formats()
	sort.Strings(formats)
	return strings.Join(formats, ", ")
}

func (r *Runner) diagnose(format string, args ...any) {
	if r.opts.Diagnostics == nil {
		return
	}
	fmt.Fprintf(r.opts.Diagnostics, format, args...)
}
