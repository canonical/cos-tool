// Package batch implements the batch transform interface: many label-injection
// requests processed in a single cos-tool invocation, exchanged as JSONL files.
//
// See docs/adr/0001-batch-cli-mode-for-python-integration.md for the decision
// and docs/design/0001-batch-transform-interfaces.md for the module design.
//
// The package owns the entire wire interface and the batch loop. It knows
// nothing about the CLI, file paths, or process exit: callers hand it an
// io.Reader of requests and an io.Writer for results.
package batch

import (
	"encoding/json"
	"fmt"
)

// Version is the batch interface version this binary implements.
//
// Every result line reports the version that produced it, so a results file is
// self-describing. A request declaring a higher version is reported per record
// as StatusUnsupportedVersion rather than failing the batch.
const Version = 1

// Matchers is a set of label matchers to inject, keyed by label name.
//
// It is a named type so that the decoding rule lives with the data: a JSON
// null value is rejected rather than silently coerced to the empty string.
// That keeps null free as the future encoding for "unset this inherited
// default", which v1 deliberately does not support.
type Matchers map[string]string

// UnmarshalJSON decodes a matcher object, rejecting null values.
func (m *Matchers) UnmarshalJSON(data []byte) error {
	var raw map[string]*string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	out := make(Matchers, len(raw))
	for name, value := range raw {
		if value == nil {
			return fmt.Errorf("matcher %q has a null value; a label value must be a string", name)
		}
		out[name] = *value
	}

	*m = out
	return nil
}

// Request is one input line: an expression to transform plus the context
// needed to transform it.
//
// The required fields are pointers so that absent can be told from present but
// empty. This is load-bearing: the contract promises StatusMalformed for a
// missing id or expr, which a plain string cannot distinguish from "".
type Request struct {
	ID       *string  `json:"id"`
	Expr     *string  `json:"expr"`
	Matchers Matchers `json:"matchers,omitempty"`
	Format   *string  `json:"format,omitempty"`
	Version  *int     `json:"version,omitempty"`
}

// Status is the per-record outcome vocabulary.
type Status string

const (
	// StatusOK means the expression was transformed; Result.Expr holds it.
	StatusOK Status = "ok"

	// StatusError means the expression could not be parsed, or transforming it
	// failed. It is a problem with the expression, not with the request.
	StatusError Status = "error"

	// StatusMalformed means the line could not be read as a request at all:
	// invalid JSON, a missing required field, or an unusable field value.
	StatusMalformed Status = "malformed"

	// StatusUnsupportedVersion means the request is well formed but declares an
	// interface version this binary does not implement.
	StatusUnsupportedVersion Status = "unsupported_version"
)

// Result is one output line.
//
// Field order is the emitted JSON key order and matches the examples in
// README.md. Keep it stable: callers read these files by eye when debugging.
type Result struct {
	// Version is always this binary's Version, the version that produced the
	// result, never the version the request declared.
	Version int `json:"version"`

	// ID echoes the request's id. It is a pointer so that an empty id is
	// echoed back as "" rather than vanishing under omitempty, and is omitted
	// only when the id could not be recovered at all.
	ID *string `json:"id,omitempty"`

	// Line is the 1-based input line number. It is reported on every failure
	// so a caller can find the offending line in a large batch file, and
	// omitted on success, where it would be redundant on every one of 50,000
	// lines.
	Line int `json:"line,omitempty"`

	Status Status `json:"status"`

	// Expr is the transformed expression; set only when Status is StatusOK.
	Expr string `json:"expr,omitempty"`

	// Error describes the failure; set only when Status is not StatusOK.
	Error string `json:"error,omitempty"`
}
