package batch

import (
	"bufio"
	"encoding/json"
	"io"
)

// Encoder writes results to a JSONL stream, one line per result.
//
// Writes are buffered, so Flush must be called before the output is complete.
// Its error must be propagated: unflushed output is the one way a batch can
// look successful while being short, which the exit status contract forbids.
type Encoder struct {
	w   *bufio.Writer
	enc *json.Encoder
}

// NewEncoder returns an Encoder writing results to w.
func NewEncoder(w io.Writer) *Encoder {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)

	// Expressions routinely contain <, > and & (comparison operators and
	// regex alternation). Escaping them to \u003c and friends would be valid
	// JSON but would make results files unreadable by eye, and the output is
	// never embedded in HTML.
	enc.SetEscapeHTML(false)

	return &Encoder{w: bw, enc: enc}
}

// Encode writes exactly one line for res.
//
// json.Encoder terminates each value with a newline, which is what gives the
// one-result-per-line framing.
func (e *Encoder) Encode(res Result) error {
	return e.enc.Encode(res)
}

// Flush pushes buffered output to the underlying writer.
func (e *Encoder) Flush() error {
	return e.w.Flush()
}
