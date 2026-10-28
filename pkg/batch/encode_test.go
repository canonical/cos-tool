package batch_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr(s string) *string { return &s }

func TestEncoderWritesOneLinePerResult(t *testing.T) {
	var buf bytes.Buffer
	enc := batch.NewEncoder(&buf)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, enc.Encode(batch.Result{
			Version: batch.Version, ID: ptr(id), Status: batch.StatusOK, Expr: "up",
		}))
	}
	require.NoError(t, enc.Flush())

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	assert.Len(t, lines, 3)
	for _, line := range lines {
		assert.NotEmpty(t, line)
	}
}

func TestEncoderDoesNotEscapeComparisonOperators(t *testing.T) {
	// Escaping < and > to \u003c would be valid JSON but would make a results
	// file unreadable by eye, and it is never embedded in HTML.
	var buf bytes.Buffer
	enc := batch.NewEncoder(&buf)

	require.NoError(t, enc.Encode(batch.Result{
		Version: batch.Version,
		ID:      ptr("a"),
		Status:  batch.StatusOK,
		Expr:    `rate(http_requests_total[5m]) > 0.5 and x < 3 and y =~ "a&b"`,
	}))
	require.NoError(t, enc.Flush())

	assert.Contains(t, buf.String(), `> 0.5`)
	assert.Contains(t, buf.String(), `< 3`)
	assert.Contains(t, buf.String(), `a&b`)
	assert.NotContains(t, buf.String(), `\u003e`)
	assert.NotContains(t, buf.String(), `\u0026`)
}

func TestEncoderNothingWrittenBeforeFlush(t *testing.T) {
	var buf bytes.Buffer
	enc := batch.NewEncoder(&buf)

	require.NoError(t, enc.Encode(batch.Result{
		Version: batch.Version, ID: ptr("a"), Status: batch.StatusOK, Expr: "up",
	}))
	assert.Empty(t, buf.String(), "output is buffered, so flush is load-bearing")

	require.NoError(t, enc.Flush())
	assert.NotEmpty(t, buf.String())
}

// failingWriter accepts a fixed number of bytes and then fails.
type failingWriter struct {
	remaining int
	err       error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, f.err
	}
	if len(p) > f.remaining {
		n := f.remaining
		f.remaining = 0
		return n, f.err
	}
	f.remaining -= len(p)
	return len(p), nil
}

func TestEncoderFlushSurfacesWriteFailure(t *testing.T) {
	// The important case: a write that fails only at flush time must be
	// reported, never swallowed into a quietly short results file.
	wantErr := errors.New("no space left on device")
	enc := batch.NewEncoder(&failingWriter{remaining: 0, err: wantErr})

	// Buffered, so this call typically succeeds; the failure lands on flush.
	_ = enc.Encode(batch.Result{
		Version: batch.Version, ID: ptr("a"), Status: batch.StatusOK, Expr: "up",
	})

	assert.ErrorIs(t, enc.Flush(), wantErr)
}
