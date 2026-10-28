package batch_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collect drains a decoder, returning the records and any read fault.
func collect(t *testing.T, r io.Reader) ([]batch.Record, error) {
	t.Helper()

	dec := batch.NewDecoder(r)
	var records []batch.Record
	for {
		rec, ok := dec.Next()
		if !ok {
			break
		}
		records = append(records, rec)
	}
	return records, dec.Err()
}

func TestDecoderFraming(t *testing.T) {
	t.Run("empty input yields no records and no error", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(""))
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("trailing newline does not produce a phantom record", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(
			`{"id":"a","expr":"up"}`+"\n"+`{"id":"b","expr":"up"}`+"\n"))
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("final line without trailing newline is read", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(
			`{"id":"a","expr":"up"}`+"\n"+`{"id":"b","expr":"up"}`))
		require.NoError(t, err)
		require.Len(t, records, 2)
		require.NotNil(t, records[1].Req)
		assert.Equal(t, "b", *records[1].Req.ID)
	})

	t.Run("line numbers are 1-based and sequential", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(
			`{"id":"a","expr":"up"}`+"\n"+`garbage`+"\n"+`{"id":"c","expr":"up"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 3)
		assert.Equal(t, 1, records[0].Line)
		assert.Equal(t, 2, records[1].Line)
		assert.Equal(t, 3, records[2].Line)
	})

	t.Run("CRLF line endings are tolerated", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(`{"id":"a","expr":"up"}`+"\r\n"))
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.NotNil(t, records[0].Req, "a CRLF line must still decode")
		assert.Equal(t, "a", *records[0].Req.ID)
	})

	t.Run("very long lines are not truncated or rejected", func(t *testing.T) {
		// Comfortably past bufio.Scanner's 64KB default, which is exactly the
		// cliff this decoder exists to avoid.
		longValue := strings.Repeat("x", 512*1024)
		records, err := collect(t, strings.NewReader(
			`{"id":"long","expr":"`+longValue+`"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.NotNil(t, records[0].Req)
		assert.Len(t, *records[0].Req.Expr, len(longValue))
	})

	t.Run("embedded newlines inside strings do not break framing", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(`{"id":"a","expr":"up\nor\ndown"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.NotNil(t, records[0].Req)
		assert.Equal(t, "up\nor\ndown", *records[0].Req.Expr)
	})
}

func TestDecoderLineFaults(t *testing.T) {
	t.Run("a bad line does not stop later lines", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(
			`{"id":"a","expr":"up"}`+"\n"+
				`{not json`+"\n"+
				`{"id":"c","expr":"up"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 3)

		assert.NotNil(t, records[0].Req)
		assert.Error(t, records[1].Fault)
		assert.Nil(t, records[1].Req)
		assert.NotNil(t, records[2].Req, "a bad line must not cost the lines after it")
	})

	t.Run("id is recovered from a line that fails to decode", func(t *testing.T) {
		// Wrong type on expr, but the id is still readable and worth returning.
		records, err := collect(t, strings.NewReader(`{"id":"rule-9","expr":123}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Error(t, records[0].Fault)
		require.NotNil(t, records[0].RecoveredID)
		assert.Equal(t, "rule-9", *records[0].RecoveredID)
	})

	t.Run("unrecoverable id stays nil", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(`{"id":[1,2],"expr":"up"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 1)
		require.Error(t, records[0].Fault)
		assert.Nil(t, records[0].RecoveredID)
	})

	t.Run("blank and whitespace-only lines are faults", func(t *testing.T) {
		records, err := collect(t, strings.NewReader(
			`{"id":"a","expr":"up"}`+"\n"+"\n"+"   \t\n"+`{"id":"d","expr":"up"}`+"\n"))
		require.NoError(t, err)
		require.Len(t, records, 4, "blank lines must still produce a record, to keep line parity")
		assert.Error(t, records[1].Fault)
		assert.Error(t, records[2].Fault)
		assert.NotNil(t, records[3].Req)
	})

	t.Run("non-object JSON is a fault", func(t *testing.T) {
		for _, line := range []string{`null`, `[]`, `"a string"`, `42`, `true`} {
			records, err := collect(t, strings.NewReader(line+"\n"))
			require.NoError(t, err)
			require.Len(t, records, 1)
			assert.Error(t, records[0].Fault, "%s must not decode as a request", line)
		}
	})
}

// failingReader yields some valid input and then fails, standing in for a
// truncated or unreadable file.
type failingReader struct {
	prefix string
	err    error
	done   bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.done {
		return 0, f.err
	}
	f.done = true
	return copy(p, f.prefix), nil
}

func TestDecoderReadFault(t *testing.T) {
	// A read fault is not a line fault: it means the batch cannot complete,
	// so it surfaces via Err rather than as a malformed record.
	wantErr := errors.New("disk fell over")
	records, err := collect(t, &failingReader{
		prefix: `{"id":"a","expr":"up"}` + "\n",
		err:    wantErr,
	})

	require.Len(t, records, 1)
	assert.NotNil(t, records[0].Req)
	assert.ErrorIs(t, err, wantErr)
}
