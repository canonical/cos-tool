package root_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The CLI is exercised through the built binary rather than in-process,
// because the contract being tested is largely about exit status and about
// which files exist afterwards.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestBatchVersionFlag(t *testing.T) {
	bin := buildBinary(t)

	stdout, stderr, code := runTool(t, bin, "transform", "--batch-version")

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, strings.TrimSpace(stdout), "1")
}

func TestBatchVersionTouchesNoFiles(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "results.jsonl")

	_, _, code := runTool(t, bin, "transform", "--batch-version", "--out", out)
	assert.Equal(t, 0, code)
	assert.NoFileExists(t, out, "--batch-version must be a side-effect-free probe")
}

func TestUsageErrorsCreateNoFile(t *testing.T) {
	bin := buildBinary(t)

	tests := []struct {
		name string
		args func(dir string) []string
	}{
		{
			name: "batch without out",
			args: func(dir string) []string {
				return []string{"transform", "--batch", writeBatch(nil, dir)}
			},
		},
		{
			name: "out without batch",
			args: func(dir string) []string {
				return []string{"transform", "--out", filepath.Join(dir, "results.jsonl")}
			},
		},
		{
			name: "batch alongside an expression",
			args: func(dir string) []string {
				return []string{
					"transform",
					"--batch", writeBatch(nil, dir),
					"--out", filepath.Join(dir, "results.jsonl"),
					"--", "up == 0",
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, stderr, code := runTool(t, bin, tc.args(dir)...)

			assert.NotEqual(t, 0, code, "a usage error must exit non-zero")
			assert.NotEmpty(t, stderr, "a usage error must explain itself")
			assert.NoFileExists(t, filepath.Join(dir, "results.jsonl"),
				"no results file may be created for a usage error")
		})
	}
}

func TestSingleExpressionPathUnchanged(t *testing.T) {
	bin := buildBinary(t)

	stdout, stderr, code := runTool(t, bin,
		"--format", "promql", "transform",
		"--label-matcher", "juju_model=cos",
		"--", `rate(http_requests_total{job="myjob"}[5m]) > 0.5`)

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t,
		`rate(http_requests_total{job="myjob",juju_model="cos"}[5m]) > 0.5`,
		stdout, "the single-expression path must be byte-identical and unterminated")
}

func TestBatchHappyPath(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch([]string{
		`{"id":"rule-42","expr":"rate(http_requests_total{job=\"myjob\"}[5m]) > 0.5","matchers":{"juju_application":"proxy"}}`,
		`{"id":"rule-43","expr":"sum(rate({job=\"nginx\"}[5m]))","format":"logql"}`,
		`{"id":"rule-44","expr":"up == 0"}`,
		`{"id":"rule-45","expr":"rate(bad{{[5m])"}`,
	}, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	stdout, stderr, code := runTool(t, bin,
		"--format", "promql", "transform",
		"--batch", batchPath, "--out", outPath,
		"--label-matcher", "juju_model=cos")

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Empty(t, stdout, "result data must never go to stdout")

	results := readResults(t, outPath)
	require.Len(t, results, 4, "exactly one result per request line")

	assert.Equal(t, batch.StatusOK, results[0].Status)
	assert.Contains(t, results[0].Expr, `juju_application="proxy"`)
	assert.Contains(t, results[0].Expr, `juju_model="cos"`)

	assert.Equal(t, batch.StatusOK, results[1].Status)
	assert.Equal(t, batch.StatusOK, results[2].Status)

	// One invalid expression costs only its own record.
	assert.Equal(t, batch.StatusError, results[3].Status)
	assert.Contains(t, results[3].Error, "parse error")
}

func TestBatchExitZeroDespitePerRecordFailures(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch([]string{
		`not json`,
		`{"id":"a"}`,
		`{"id":"b","expr":"rate(bad{{[5m])"}`,
		`{"id":"c","expr":"up","version":99}`,
	}, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", batchPath, "--out", outPath)

	require.Equal(t, 0, code,
		"exit status reports completion, not per-expression success; stderr: %s", stderr)

	results := readResults(t, outPath)
	require.Len(t, results, 4)
	assert.Equal(t, batch.StatusMalformed, results[0].Status)
	assert.Equal(t, batch.StatusMalformed, results[1].Status)
	assert.Equal(t, batch.StatusError, results[2].Status)
	assert.Equal(t, batch.StatusUnsupportedVersion, results[3].Status)
}

func TestBatchEmptyFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch(nil, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", batchPath, "--out", outPath)

	require.Equal(t, 0, code, "an empty batch is valid; stderr: %s", stderr)
	require.FileExists(t, outPath)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Empty(t, data, "an empty batch produces an empty results file")
}

func TestBatchMissingInputFails(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "results.jsonl")

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", filepath.Join(dir, "does-not-exist.jsonl"), "--out", outPath)

	assert.NotEqual(t, 0, code, "an unreadable batch file means the batch did not complete")
	assert.NotEmpty(t, stderr)
}

func TestBatchOverwritesExistingResults(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch([]string{`{"id":"a","expr":"up"}`}, dir)
	outPath := filepath.Join(dir, "results.jsonl")
	require.NoError(t, os.WriteFile(outPath, []byte("stale content from a previous run\n"), 0o600))

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", batchPath, "--out", outPath)

	require.Equal(t, 0, code, "stderr: %s", stderr)
	results := readResults(t, outPath)
	require.Len(t, results, 1, "the results file is truncated, not appended to")
	assert.Equal(t, batch.StatusOK, results[0].Status)
}

func TestBatchPerRecordMatchersOverrideDefaults(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch([]string{
		`{"id":"a","expr":"up","matchers":{"juju_model":"override"}}`,
		`{"id":"b","expr":"up"}`,
	}, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", batchPath, "--out", outPath,
		"--label-matcher", "juju_model=cos")

	require.Equal(t, 0, code, "stderr: %s", stderr)

	results := readResults(t, outPath)
	require.Len(t, results, 2)
	assert.Contains(t, results[0].Expr, `juju_model="override"`)
	assert.Contains(t, results[1].Expr, `juju_model="cos"`)
}

func TestBatchUnknownFormatFlagDoesNotPoisonEveryRecord(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	batchPath := writeBatch([]string{`{"id":"a","expr":"up"}`}, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	// An unrecognised --format has always fallen back to promql rather than
	// being rejected; it must not turn every record malformed.
	_, stderr, code := runTool(t, bin, "--format", "nonsense", "transform",
		"--batch", batchPath, "--out", outPath)

	require.Equal(t, 0, code, "stderr: %s", stderr)
	results := readResults(t, outPath)
	require.Len(t, results, 1)
	assert.Equal(t, batch.StatusOK, results[0].Status)
}

func TestBatchLargeInputKeepsLineParity(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	const records = 5000
	lines := make([]string, 0, records)
	for i := 0; i < records; i++ {
		lines = append(lines, `{"id":"rule-`+itoa(i)+`","expr":"up{job=\"j`+itoa(i)+`\"} == 0"}`)
	}
	batchPath := writeBatch(lines, dir)
	outPath := filepath.Join(dir, "results.jsonl")

	_, stderr, code := runTool(t, bin, "transform",
		"--batch", batchPath, "--out", outPath,
		"--label-matcher", "juju_model=cos")

	require.Equal(t, 0, code, "stderr: %s", stderr)

	results := readResults(t, outPath)
	require.Len(t, results, records, "line parity must hold at volume")
	for i, res := range results {
		require.Equal(t, batch.StatusOK, res.Status)
		assert.Equal(t, "rule-"+itoa(i), *res.ID, "results must be in input order")
	}
}

// readResults parses a results file into decoded results.
func readResults(t *testing.T, path string) []batch.Result {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var results []batch.Result
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var res batch.Result
		require.NoError(t, json.Unmarshal([]byte(line), &res), "invalid result line: %s", line)
		require.Equal(t, batch.Version, res.Version)
		results = append(results, res)
	}
	return results
}

// writeBatch writes lines to a batch file in dir and returns its path.
func writeBatch(lines []string, dir string) string {
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	path := filepath.Join(dir, "requests.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
	return path
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
