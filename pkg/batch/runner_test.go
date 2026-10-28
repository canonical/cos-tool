package batch_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoTransformer appends the merged matchers to the expression, which is
// enough to assert merge behaviour without depending on a real parser.
type echoTransformer struct {
	name string
}

func (e *echoTransformer) Transform(expr string, matchers *map[string]string) (string, error) {
	if strings.Contains(expr, "BOOM") {
		panic("placeholder restoration exploded")
	}
	if strings.Contains(expr, "INVALID") {
		return expr, errors.New("1:10: parse error: unexpected left brace '{'")
	}

	keys := make([]string, 0, len(*matchers))
	for key := range *matchers {
		keys = append(keys, key+"="+(*matchers)[key])
	}
	// Sorted for deterministic assertions.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	return fmt.Sprintf("%s[%s]{%s}", expr, e.name, strings.Join(keys, ",")), nil
}

func testResolver() batch.Resolver {
	return batch.NewMapResolver(map[string]batch.Transformer{
		"promql": &echoTransformer{name: "promql"},
		"logql":  &echoTransformer{name: "logql"},
	})
}

// run executes a batch over the given input lines and returns the decoded
// results plus the raw output.
func run(t *testing.T, opts batch.Options, input string) ([]batch.Result, string) {
	t.Helper()

	if opts.DefaultFormat == "" {
		opts.DefaultFormat = "promql"
	}

	var out bytes.Buffer
	runner := batch.NewRunner(opts, testResolver())
	require.NoError(t, runner.Run(strings.NewReader(input), &out))

	var results []batch.Result
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var res batch.Result
		require.NoError(t, json.Unmarshal([]byte(line), &res), "output must be valid JSONL")
		results = append(results, res)
	}
	return results, out.String()
}

func TestRunnerLineParity(t *testing.T) {
	// The central invariant: exactly one result per input line, in input
	// order, regardless of how many lines are bad.
	input := strings.Join([]string{
		`{"id":"ok-1","expr":"up"}`,
		`not json at all`,
		`{"id":"bad-expr","expr":"INVALID"}`,
		`{"id":"no-expr"}`,
		`{"expr":"up"}`,
		`{"id":"future","expr":"up","version":99}`,
		`{"id":"ok-2","expr":"up","format":"logql"}`,
		``,
		`{"id":"ok-3","expr":"up"}`,
	}, "\n") + "\n"

	results, _ := run(t, batch.Options{}, input)

	require.Len(t, results, 9, "one result per input line")
	for i, res := range results {
		assert.Equal(t, batch.Version, res.Version, "result %d must report the producing version", i)
		assert.NotEmpty(t, res.Status)
	}

	assert.Equal(t, []batch.Status{
		batch.StatusOK,
		batch.StatusMalformed,
		batch.StatusError,
		batch.StatusMalformed,
		batch.StatusMalformed,
		batch.StatusUnsupportedVersion,
		batch.StatusOK,
		batch.StatusMalformed,
		batch.StatusOK,
	}, statuses(results), "results must be in input order")
}

func statuses(results []batch.Result) []batch.Status {
	out := make([]batch.Status, 0, len(results))
	for _, res := range results {
		out = append(out, res.Status)
	}
	return out
}

func TestRunnerEmptyBatch(t *testing.T) {
	// Documented as valid: an empty results file and a successful exit.
	results, raw := run(t, batch.Options{}, "")
	assert.Empty(t, results)
	assert.Empty(t, raw)
}

func TestRunnerMatcherMerging(t *testing.T) {
	opts := batch.Options{
		DefaultMatchers: map[string]string{"juju_model": "cos", "juju_model_uuid": "12345"},
	}

	t.Run("record extends batch-wide defaults", func(t *testing.T) {
		results, _ := run(t, opts, `{"id":"a","expr":"up","matchers":{"juju_application":"proxy"}}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t,
			"up[promql]{juju_application=proxy,juju_model=cos,juju_model_uuid=12345}",
			results[0].Expr)
	})

	t.Run("record with no matchers inherits only the defaults", func(t *testing.T) {
		results, _ := run(t, opts, `{"id":"a","expr":"up"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, "up[promql]{juju_model=cos,juju_model_uuid=12345}", results[0].Expr)
	})

	t.Run("record overrides a default", func(t *testing.T) {
		results, _ := run(t, opts, `{"id":"a","expr":"up","matchers":{"juju_model":"other"}}`+"\n")
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Expr, "juju_model=other")
		assert.NotContains(t, results[0].Expr, "juju_model=cos")
	})

	t.Run("defaults do not leak between records", func(t *testing.T) {
		results, _ := run(t, opts,
			`{"id":"a","expr":"up","matchers":{"juju_application":"proxy"}}`+"\n"+
				`{"id":"b","expr":"up"}`+"\n")
		require.Len(t, results, 2)
		assert.NotContains(t, results[1].Expr, "juju_application",
			"one record's matchers must not contaminate the next")
	})

	t.Run("no matchers at all is a parse-and-normalize pass", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusOK, results[0].Status)
		assert.Equal(t, "up[promql]{}", results[0].Expr)
	})
}

func TestRunnerFormatSelection(t *testing.T) {
	t.Run("defaults to the invocation format", func(t *testing.T) {
		results, _ := run(t, batch.Options{DefaultFormat: "logql"}, `{"id":"a","expr":"up"}`+"\n")
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Expr, "[logql]")
	})

	t.Run("per-record format overrides it", func(t *testing.T) {
		results, _ := run(t, batch.Options{DefaultFormat: "promql"},
			`{"id":"a","expr":"up","format":"logql"}`+"\n")
		require.Len(t, results, 1)
		assert.Contains(t, results[0].Expr, "[logql]")
	})

	t.Run("formats can be mixed within one batch", func(t *testing.T) {
		results, _ := run(t, batch.Options{DefaultFormat: "promql"},
			`{"id":"a","expr":"up"}`+"\n"+`{"id":"b","expr":"up","format":"logql"}`+"\n")
		require.Len(t, results, 2)
		assert.Contains(t, results[0].Expr, "[promql]")
		assert.Contains(t, results[1].Expr, "[logql]")
	})

	t.Run("format is case-insensitive", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up","format":"LogQL"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusOK, results[0].Status)
	})

	t.Run("unknown format is malformed, listing the valid names", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up","format":"sql"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusMalformed, results[0].Status)
		assert.Contains(t, results[0].Error, `unknown format "sql"`)
		assert.Contains(t, results[0].Error, "logql")
		assert.Contains(t, results[0].Error, "promql")
		assert.Equal(t, 1, results[0].Line)
	})
}

func TestRunnerStatusDetails(t *testing.T) {
	t.Run("ok carries expr and no error", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusOK, results[0].Status)
		assert.NotEmpty(t, results[0].Expr)
		assert.Empty(t, results[0].Error)
		assert.Zero(t, results[0].Line, "line is redundant on success")
	})

	t.Run("error carries the parse message and never echoes the input", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"INVALID"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusError, results[0].Status)
		assert.Contains(t, results[0].Error, "parse error")
		assert.Empty(t, results[0].Expr, "a failed transform must not return the input as a result")
		assert.Equal(t, 1, results[0].Line)
	})

	t.Run("malformed reports the line number", func(t *testing.T) {
		results, _ := run(t, batch.Options{},
			`{"id":"a","expr":"up"}`+"\n"+`{{{`+"\n")
		require.Len(t, results, 2)
		assert.Equal(t, batch.StatusMalformed, results[1].Status)
		assert.Equal(t, 2, results[1].Line)
	})

	t.Run("malformed recovers the id when it can", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"rule-9","expr":123}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusMalformed, results[0].Status)
		require.NotNil(t, results[0].ID)
		assert.Equal(t, "rule-9", *results[0].ID)
	})

	t.Run("missing id names the field and omits id", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"expr":"up"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusMalformed, results[0].Status)
		assert.Contains(t, results[0].Error, `"id"`)
		assert.Nil(t, results[0].ID)
	})

	t.Run("missing expr names the field and keeps the id", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a"}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusMalformed, results[0].Status)
		assert.Contains(t, results[0].Error, `"expr"`)
		require.NotNil(t, results[0].ID)
		assert.Equal(t, "a", *results[0].ID)
	})

	t.Run("null matcher value is malformed, not coerced", func(t *testing.T) {
		results, _ := run(t, batch.Options{},
			`{"id":"a","expr":"up","matchers":{"juju_model":null}}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusMalformed, results[0].Status)
	})

	t.Run("explicit current version is accepted", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up","version":1}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusOK, results[0].Status)
	})

	t.Run("unsupported version is per-record and keeps the id", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up","version":99}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusUnsupportedVersion, results[0].Status)
		assert.Equal(t, batch.Version, results[0].Version, "the result reports the binary's version")
		require.NotNil(t, results[0].ID)
		assert.Contains(t, results[0].Error, "99")
	})

	t.Run("version is checked before required fields", func(t *testing.T) {
		// A future record omitting a v1-required field should be reported as
		// a version problem, which is actionable, not as malformed.
		results, _ := run(t, batch.Options{}, `{"id":"a","version":99}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusUnsupportedVersion, results[0].Status)
	})

	t.Run("unknown fields are ignored", func(t *testing.T) {
		results, _ := run(t, batch.Options{}, `{"id":"a","expr":"up","future":{"x":1}}`+"\n")
		require.Len(t, results, 1)
		assert.Equal(t, batch.StatusOK, results[0].Status)
	})
}

func TestRunnerPanicBarrier(t *testing.T) {
	// A panic in cos-tool's own transform code must cost only its own record;
	// the batch's other results are the whole point of batch mode.
	var diagnostics bytes.Buffer
	var out bytes.Buffer

	runner := batch.NewRunner(batch.Options{
		DefaultFormat: "promql",
		Diagnostics:   &diagnostics,
	}, testResolver())

	input := `{"id":"a","expr":"up"}` + "\n" +
		`{"id":"b","expr":"BOOM"}` + "\n" +
		`{"id":"c","expr":"up"}` + "\n"

	require.NoError(t, runner.Run(strings.NewReader(input), &out),
		"a recovered panic must not fail the invocation")

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 3)

	var panicked batch.Result
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &panicked))
	assert.Equal(t, batch.StatusError, panicked.Status)
	assert.Contains(t, panicked.Error, "internal error")

	assert.Contains(t, diagnostics.String(), "internal error",
		"the bug must stay visible on stderr")
}

func TestRunnerReadFaultFailsTheBatch(t *testing.T) {
	wantErr := errors.New("input truncated")
	var out bytes.Buffer

	runner := batch.NewRunner(batch.Options{DefaultFormat: "promql"}, testResolver())
	err := runner.Run(&failingReader{prefix: `{"id":"a","expr":"up"}` + "\n", err: wantErr}, &out)

	require.Error(t, err, "an unreadable batch means the invocation did not complete")
	assert.ErrorIs(t, err, wantErr)
}

func TestRunnerWriteFaultFailsTheBatch(t *testing.T) {
	wantErr := errors.New("no space left on device")
	runner := batch.NewRunner(batch.Options{DefaultFormat: "promql"}, testResolver())

	err := runner.Run(
		strings.NewReader(strings.Repeat(`{"id":"a","expr":"up"}`+"\n", 5000)),
		&failingWriter{remaining: 100, err: wantErr},
	)

	require.Error(t, err, "an unwritable results file means the invocation did not complete")
	assert.ErrorIs(t, err, wantErr)
}

func TestRunnerDiagnosticsOptional(t *testing.T) {
	// Nil Diagnostics must not turn a recovered panic into a nil-pointer panic.
	var out bytes.Buffer
	runner := batch.NewRunner(batch.Options{DefaultFormat: "promql"}, testResolver())
	require.NoError(t, runner.Run(strings.NewReader(`{"id":"b","expr":"BOOM"}`+"\n"), &out))
	assert.Contains(t, out.String(), "internal error")
}
