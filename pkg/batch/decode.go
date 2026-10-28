package batch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Record is one decoded input line.
//
// Exactly one of Req and Fault is set. A Fault is a line fault: this line
// could not be read as a request, but the batch continues. Read faults, which
// end the batch, are reported by Decoder.Err instead.
type Record struct {
	// Line is the 1-based input line number.
	Line int

	// Req is the decoded request, or nil when Fault is set.
	Req *Request

	// Fault explains why this line is not a request. Nil when Req is set.
	Fault error

	// RecoveredID is the request's id when Fault is set but the id could still
	// be read, which lets a caller correlate a malformed line with its own
	// data instead of only its line number. Always nil when Req is set, where
	// the id is on Req.
	//
	// Not in the original design, which gave Record only Line, Req and Fault;
	// without it the documented "id is included when it could be" behaviour
	// for malformed records is unimplementable.
	RecoveredID *string
}

// Decoder reads requests from a JSONL stream, one per line.
//
// Lines are read to the next newline with no length limit and unmarshalled
// individually, so a bad line costs only itself. A streaming json.Decoder
// cannot be used here: one malformed line desynchronises it, losing both
// per-line recovery and accurate line numbers.
type Decoder struct {
	r    *bufio.Reader
	line int
	err  error
}

// NewDecoder returns a Decoder reading requests from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Next returns the next record, or ok=false at clean end of input or on a read
// fault. Check Err to tell those apart.
func (d *Decoder) Next() (Record, bool) {
	if d.err != nil {
		return Record{}, false
	}

	raw, err := d.r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		d.err = err
		return Record{}, false
	}

	// A final line need not be newline-terminated. Only an EOF with nothing
	// buffered is the end of input, which also means a trailing newline on the
	// last record does not produce a phantom record.
	if raw == "" {
		return Record{}, false
	}

	d.line++
	return decodeLine(d.line, raw), true
}

// Err reports a read fault that means the batch cannot complete. It is nil
// after a clean end of input.
func (d *Decoder) Err() error {
	return d.err
}

// decodeLine turns one raw line into a Record.
func decodeLine(line int, raw string) Record {
	trimmed := strings.TrimRight(raw, "\r\n")

	if strings.TrimSpace(trimmed) == "" {
		return Record{
			Line:  line,
			Fault: errors.New("blank line"),
		}
	}

	// Reject non-objects explicitly. Unmarshalling a bare "null" into a struct
	// succeeds and leaves every field unset, which would otherwise be reported
	// as missing fields rather than as the wrong shape.
	if leading := strings.TrimLeft(trimmed, " \t"); !strings.HasPrefix(leading, "{") {
		return Record{
			Line:        line,
			Fault:       errors.New("not a JSON object"),
			RecoveredID: nil,
		}
	}

	var req Request
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return Record{
			Line:        line,
			Fault:       fmt.Errorf("invalid request: %w", err),
			RecoveredID: recoverID(trimmed),
		}
	}

	return Record{Line: line, Req: &req}
}

// recoverID extracts just the id from a line that failed to decode as a
// request, so a malformed record can still be correlated by the caller.
// Returns nil when the id itself is absent or unusable.
func recoverID(line string) *string {
	var partial struct {
		ID *string `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &partial); err != nil {
		return nil
	}
	return partial.ID
}
