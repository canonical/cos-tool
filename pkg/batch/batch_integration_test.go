package batch_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realResolver wires the actual PromQL and LogQL transformers.
func realResolver() batch.Resolver {
	return batch.NewMapResolver(map[string]batch.Transformer{
		"promql": &tool.PromQL{},
		"logql":  &tool.LogQL{},
	})
}

// TestReadmeExample reproduces the worked example from README.md verbatim.
// If this breaks, either the code or the documentation is wrong.
func TestReadmeExample(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"rule-42","expr":"rate(http_requests_total{job=\"myjob\"}[5m]) > 0.5","matchers":{"juju_application":"proxy","juju_unit":"proxy/1"}}`,
		`{"id":"rule-43","expr":"sum(rate({job=\"nginx\"}[5m]))","matchers":{"juju_application":"nginx"},"format":"logql"}`,
		`{"id":"rule-44","expr":"up == 0"}`,
		`{"id":"rule-45","expr":"rate(bad{{[5m])"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	runner := batch.NewRunner(batch.Options{
		DefaultFormat:   "promql",
		DefaultMatchers: map[string]string{"juju_model": "cos", "juju_model_uuid": "12345"},
	}, realResolver())

	require.NoError(t, runner.Run(strings.NewReader(input), &out))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, 4)

	assert.JSONEq(t,
		`{"version":1,"id":"rule-42","status":"ok","expr":"rate(http_requests_total{job=\"myjob\",juju_application=\"proxy\",juju_model=\"cos\",juju_model_uuid=\"12345\",juju_unit=\"proxy/1\"}[5m]) > 0.5"}`,
		lines[0])

	assert.JSONEq(t,
		`{"version":1,"id":"rule-43","status":"ok","expr":"sum(rate({job=\"nginx\", juju_application=\"nginx\", juju_model=\"cos\", juju_model_uuid=\"12345\"}[5m]))"}`,
		lines[1])

	assert.JSONEq(t,
		`{"version":1,"id":"rule-44","status":"ok","expr":"up{juju_model=\"cos\",juju_model_uuid=\"12345\"} == 0"}`,
		lines[2])

	// The invalid expression fails only its own record.
	var failed batch.Result
	require.NoError(t, json.Unmarshal([]byte(lines[3]), &failed))
	assert.Equal(t, batch.StatusError, failed.Status)
	assert.Equal(t, "rule-45", *failed.ID)
	assert.Contains(t, failed.Error, "parse error")
	assert.Empty(t, failed.Expr)
}

// TestGrafanaVariablesSurviveBatch checks that template-variable handling is
// identical in batch mode, as README.md promises.
func TestGrafanaVariablesSurviveBatch(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"a","expr":"rate(metric{job=\"$job\"}[$__rate_interval])"}`,
		`{"id":"b","expr":"sum by ($grouping) (rate({job=\"$job\"}[5m]))","format":"logql"}`,
		`{"id":"c","expr":"topk($limit, metric{label=\"value\"})"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	runner := batch.NewRunner(batch.Options{
		DefaultFormat:   "promql",
		DefaultMatchers: map[string]string{"juju_model": "cos"},
	}, realResolver())

	require.NoError(t, runner.Run(strings.NewReader(input), &out))

	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var res batch.Result
		require.NoError(t, json.Unmarshal([]byte(line), &res))
		require.Equal(t, batch.StatusOK, res.Status, "unexpected failure: %s", res.Error)
		assert.Contains(t, res.Expr, "juju_model=")
	}

	assert.Contains(t, out.String(), `$__rate_interval`)
	assert.Contains(t, out.String(), `$grouping`)
	assert.Contains(t, out.String(), `$limit`)
}

// TestBatchMatchesSingleExpressionPath is the strongest compatibility claim
// available: for the same input, batch mode must produce exactly what the
// single-expression path produces.
func TestBatchMatchesSingleExpressionPath(t *testing.T) {
	exprs := []string{
		`rate(http_requests_total{job="myjob"}[5m]) > 0.5`,
		`up == 0`,
		`sum by (instance) (rate(metric[5m]))`,
		`${metric:value}(otelcol_receiver_accepted{receiver=~"$receiver"}[$__rate_interval])`,
		`histogram_quantile(0.99, sum(rate(x_bucket[5m])) by (le))`,
	}
	matchers := map[string]string{"juju_model": "cos", "juju_application": "proxy"}

	var input strings.Builder
	for i, expr := range exprs {
		line, err := json.Marshal(batch.Request{
			ID:   ptr(fmt.Sprintf("r-%d", i)),
			Expr: ptr(expr),
		})
		require.NoError(t, err)
		input.Write(line)
		input.WriteString("\n")
	}

	var out bytes.Buffer
	runner := batch.NewRunner(batch.Options{
		DefaultFormat:   "promql",
		DefaultMatchers: matchers,
	}, realResolver())
	require.NoError(t, runner.Run(strings.NewReader(input.String()), &out))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, len(exprs))

	for i, expr := range exprs {
		single := &tool.PromQL{}
		perCall := map[string]string{"juju_model": "cos", "juju_application": "proxy"}
		want, err := single.Transform(expr, &perCall)
		require.NoError(t, err)

		var res batch.Result
		require.NoError(t, json.Unmarshal([]byte(lines[i]), &res))
		assert.Equal(t, want, res.Expr, "batch output must match single-expression output for %q", expr)
	}
}

// TestSequentialTransformerReuseIsClean guards the per-call receiver state
// noted in the design: reusing one transformer across many records must not
// let one record's state bleed into the next.
func TestSequentialTransformerReuseIsClean(t *testing.T) {
	const repeats = 200

	var input strings.Builder
	for i := 0; i < repeats; i++ {
		input.WriteString(fmt.Sprintf(
			`{"id":"r-%d","expr":"up{job=\"j%d\"} == 0","matchers":{"juju_unit":"u/%d"}}`+"\n", i, i, i))
	}

	var out bytes.Buffer
	runner := batch.NewRunner(batch.Options{
		DefaultFormat:   "promql",
		DefaultMatchers: map[string]string{"juju_model": "cos"},
	}, realResolver())
	require.NoError(t, runner.Run(strings.NewReader(input.String()), &out))

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, repeats)

	for i, line := range lines {
		var res batch.Result
		require.NoError(t, json.Unmarshal([]byte(line), &res))
		require.Equal(t, batch.StatusOK, res.Status)
		assert.Contains(t, res.Expr, fmt.Sprintf(`job="j%d"`, i))
		assert.Contains(t, res.Expr, fmt.Sprintf(`juju_unit="u/%d"`, i))
		assert.Contains(t, res.Expr, `juju_model="cos"`)
	}
}

// BenchmarkRunner settles the throughput debt ADR 0001 explicitly records:
// per-expression work was never measured, so it must be benchmarked once
// implemented and a target set from the numbers.
func BenchmarkRunner(b *testing.B) {
	const records = 50000

	var input strings.Builder
	for i := 0; i < records; i++ {
		input.WriteString(fmt.Sprintf(
			`{"id":"rule-%d","expr":"rate(http_requests_total{job=\"j%d\"}[5m]) > 0.5",`+
				`"matchers":{"juju_application":"app%d","juju_unit":"app%d/0"}}`+"\n", i, i, i, i))
	}
	batchFile := input.String()

	opts := batch.Options{
		DefaultFormat:   "promql",
		DefaultMatchers: map[string]string{"juju_model": "cos", "juju_model_uuid": "12345"},
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(batchFile)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runner := batch.NewRunner(opts, realResolver())
		if err := runner.Run(strings.NewReader(batchFile), &nopWriter{}); err != nil {
			b.Fatal(err)
		}
	}
}

// nopWriter discards output so the benchmark measures transformation rather
// than I/O.
type nopWriter struct{}

func (*nopWriter) Write(p []byte) (int, error) { return len(p), nil }
