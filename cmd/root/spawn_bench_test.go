// Benchmarks comparing batch mode against the per-expression invocation it
// replaces.
//
// These deliberately spawn real processes. The quantity ADR 0001 is built on
// is process-spawn overhead, which an in-process benchmark cannot see:
// pkg/batch's BenchmarkRunner measures transformation work only. The numbers
// that justified batch mode (~20ms per spawn, ~1,000s for 50,000 expressions)
// were quoted from a measurement outside the repo, so nothing here checked
// them until now.
//
// Run them with:
//
//	go test ./cmd/root/ -run '^$' -bench Spawn -benchtime 1x
//
// They are slow by nature: the consecutive case pays a fresh process per
// expression, which is the whole point. Keep -benchtime low and prefer
// BenchmarkSpawnComparison, which reports the ratio directly.
package root_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchExpressions builds n distinct PromQL expressions, each with its own
// per-rule topology labels, mirroring the real workload.
func benchExpressions(n int) []string {
	exprs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		exprs = append(exprs, fmt.Sprintf(
			`rate(http_requests_total{job="j%d"}[5m]) > 0.5`, i))
	}
	return exprs
}

// benchMatchers returns the per-expression matcher flags for expression i.
func benchMatchers(i int) []string {
	return []string{
		"--label-matcher", "juju_model=cos",
		"--label-matcher", "juju_model_uuid=12345",
		"--label-matcher", fmt.Sprintf("juju_application=app%d", i),
		"--label-matcher", fmt.Sprintf("juju_unit=app%d/0", i),
	}
}

// warmBinary runs the binary once and discards the result.
//
// The first exec of a freshly built binary pays one-off costs — on macOS,
// code-signature validation and page-cache warming — which showed up as a 20x
// outlier on the first measured iteration. Those costs are real but are paid
// once per deployment, not once per batch, so they do not belong in a
// steady-state measurement.
func warmBinary(b *testing.B, bin string) {
	b.Helper()

	if _, stderr, code := runTool(b, bin, "transform", "--batch-version"); code != 0 {
		b.Fatalf("warming binary failed: %s", stderr)
	}
}

// writeBenchBatch writes exprs as a JSONL batch file and returns its path.
func writeBenchBatch(b *testing.B, dir string, exprs []string) string {
	b.Helper()

	var content strings.Builder
	for i, expr := range exprs {
		// Expressions here contain no characters needing JSON escaping, so
		// they can be assembled directly; the correctness of escaping is
		// covered by the tests, not the benchmarks.
		content.WriteString(fmt.Sprintf(
			`{"id":"rule-%d","expr":%q,"matchers":{"juju_application":"app%d","juju_unit":"app%d/0"}}`+"\n",
			i, expr, i, i))
	}

	path := filepath.Join(dir, "requests.jsonl")
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	return path
}

// BenchmarkSpawnPerExpression measures the behaviour batch mode replaces: one
// process per expression.
func BenchmarkSpawnPerExpression(b *testing.B) {
	const records = 200 // kept small: this costs one process spawn each

	bin := buildBinary(b)
	exprs := benchExpressions(records)
	warmBinary(b, bin)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j, expr := range exprs {
			args := append([]string{"--format", "promql", "transform"}, benchMatchers(j)...)
			args = append(args, "--", expr)

			if _, stderr, code := runTool(b, bin, args...); code != 0 {
				b.Fatalf("transform failed: %s", stderr)
			}
		}
	}

	b.StopTimer()
	perExpr := time.Duration(b.Elapsed().Nanoseconds() / int64(b.N) / int64(records))
	b.ReportMetric(float64(perExpr.Nanoseconds()), "ns/expr")
}

// BenchmarkSpawnBatch measures the same work in one invocation.
func BenchmarkSpawnBatch(b *testing.B) {
	const records = 200 // matched to BenchmarkSpawnPerExpression

	bin := buildBinary(b)
	dir := b.TempDir()
	batchPath := writeBenchBatch(b, dir, benchExpressions(records))
	outPath := filepath.Join(dir, "results.jsonl")
	warmBinary(b, bin)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, stderr, code := runTool(b, bin,
			"--format", "promql", "transform",
			"--batch", batchPath, "--out", outPath,
			"--label-matcher", "juju_model=cos",
			"--label-matcher", "juju_model_uuid=12345",
		); code != 0 {
			b.Fatalf("batch transform failed: %s", stderr)
		}
	}

	b.StopTimer()
	perExpr := time.Duration(b.Elapsed().Nanoseconds() / int64(b.N) / int64(records))
	b.ReportMetric(float64(perExpr.Nanoseconds()), "ns/expr")
}

// BenchmarkSpawnComparison runs both strategies over identical input and
// reports the speedup, the measured per-spawn overhead, and an extrapolation
// to the 50,000-expression cycle ADR 0001 describes.
//
// This is the one to read. It exists because a reviewer should be able to
// check the ADR's central claim without comparing two separate benchmark
// outputs by hand.
func BenchmarkSpawnComparison(b *testing.B) {
	const (
		records   = 200
		adrVolume = 50000 // expressions per hook invocation, per ADR 0001
	)

	bin := buildBinary(b)
	exprs := benchExpressions(records)

	dir := b.TempDir()
	batchPath := writeBenchBatch(b, dir, exprs)
	outPath := filepath.Join(dir, "results.jsonl")
	warmBinary(b, bin)

	// Measured outside the b.N loop: this reports a ratio between two
	// strategies rather than the cost of one, so it runs each exactly once.
	b.ResetTimer()
	b.StopTimer()

	consecutiveStart := time.Now()
	for j, expr := range exprs {
		args := append([]string{"--format", "promql", "transform"}, benchMatchers(j)...)
		args = append(args, "--", expr)

		if _, stderr, code := runTool(b, bin, args...); code != 0 {
			b.Fatalf("transform failed: %s", stderr)
		}
	}
	consecutive := time.Since(consecutiveStart)

	batchStart := time.Now()
	if _, stderr, code := runTool(b, bin,
		"--format", "promql", "transform",
		"--batch", batchPath, "--out", outPath,
		"--label-matcher", "juju_model=cos",
		"--label-matcher", "juju_model_uuid=12345",
	); code != 0 {
		b.Fatalf("batch transform failed: %s", stderr)
	}
	batch := time.Since(batchStart)

	// A single no-op invocation isolates spawn plus runtime init, which is the
	// per-expression cost batch mode removes.
	spawnStart := time.Now()
	if _, stderr, code := runTool(b, bin, "transform", "--batch-version"); code != 0 {
		b.Fatalf("version probe failed: %s", stderr)
	}
	spawn := time.Since(spawnStart)

	perExprConsecutive := consecutive / records
	perExprBatch := batch / records
	speedup := float64(consecutive) / float64(batch)

	// Extrapolating batch cost per-expression would overstate it: the single
	// spawn is fixed, so dividing it by 200 and multiplying by 50,000 charges
	// it 250 times over. Separate the fixed cost from the marginal cost and
	// scale only the latter.
	marginalBatch := time.Duration(0)
	if batch > spawn {
		marginalBatch = (batch - spawn) / records
	}
	projectedConsecutive := perExprConsecutive * adrVolume
	projectedBatch := spawn + marginalBatch*adrVolume

	b.ReportMetric(speedup, "x-faster")
	b.ReportMetric(float64(perExprConsecutive.Nanoseconds()), "ns/expr-consecutive")
	b.ReportMetric(float64(perExprBatch.Nanoseconds()), "ns/expr-batch")
	b.ReportMetric(float64(spawn.Nanoseconds()), "ns/spawn")

	b.Logf("\n"+
		"  %d expressions, identical input and matchers\n"+
		"    consecutive:  %v total, %v per expression\n"+
		"    batch:        %v total, %v per expression\n"+
		"    speedup:      %.1fx\n"+
		"    one spawn:    %v (single no-op invocation)\n"+
		"    marginal:     %v per expression once batch mode is running\n"+
		"  projected to %d expressions (ADR 0001's cycle)\n"+
		"    consecutive:  %v  (%v x %d)\n"+
		"    batch:        %v  (one spawn + %v x %d)\n"+
		"    saved:        %v\n",
		records,
		consecutive.Round(time.Millisecond), perExprConsecutive.Round(time.Microsecond),
		batch.Round(time.Millisecond), perExprBatch.Round(time.Microsecond),
		speedup,
		spawn.Round(time.Microsecond),
		marginalBatch.Round(time.Microsecond),
		adrVolume,
		projectedConsecutive.Round(time.Second),
		perExprConsecutive.Round(time.Microsecond), adrVolume,
		projectedBatch.Round(time.Millisecond),
		marginalBatch.Round(time.Microsecond), adrVolume,
		(projectedConsecutive - projectedBatch).Round(time.Second),
	)
}
