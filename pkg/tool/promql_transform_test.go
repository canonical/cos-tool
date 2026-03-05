package tool_test

import (
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"

	"github.com/stretchr/testify/assert"
)

type TestCase struct {
	Input    string
	Matchers map[string]string
	Expected string
}

func TestShouldApplyLabelMatcherToVectorSelector(t *testing.T) {
	cases := []TestCase{
		{
			Input:    "rate(metric[5m]) > 0.5",
			Matchers: map[string]string{"bar": "baz"},
			Expected: `rate(metric{bar="baz"}[5m]) > 0.5`,
		},
		{
			Input:    "metric",
			Matchers: map[string]string{"bar": "baz"},
			Expected: `metric{bar="baz"}`,
		},
		{
			Input:    "up == 0",
			Matchers: map[string]string{"cool": "breeze", "hot": "sunrays"},
			Expected: `up{cool="breeze",hot="sunrays"} == 0`,
		},
		{
			Input:    "absent(up{job=\"prometheus\"})",
			Matchers: map[string]string{"model": "lma"},
			Expected: `absent(up{job="prometheus",model="lma"})`,
		},
		{
			Input:    `sum by(consumergroup) (kafka_consumergroup_lag) > 50`,
			Matchers: map[string]string{"firstname": "Franz"},
			Expected: `sum by (consumergroup) (kafka_consumergroup_lag{firstname="Franz"}) > 50`,
		},
		{
			Input:    `up{cool="breeze",hot="sunrays"} == 0`,
			Matchers: map[string]string{"cool": "stuff"},
			Expected: `up{cool="breeze",hot="sunrays"} == 0`,
		},
		{
			Input:    `up{cool="breeze",hot="sunrays"} == 0`,
			Matchers: map[string]string{"cool": "stuff", "dance": "macarena"},
			Expected: `up{cool="breeze",dance="macarena",hot="sunrays"} == 0`,
		},
	}
	for _, c := range cases {
		p := &tool.PromQL{}
		out, err := p.Transform(c.Input, &c.Matchers)
		assert.NoError(t, err)
		assert.Equal(t, c.Expected, out)
	}
}

func TestPromQLTransformWithVariables(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		matchers    map[string]string
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name:     "Label value variables",
			input:    `up{job="$job"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `up{env="prod",job="$job"}`,
		},
		{
			name:     "Range duration variable",
			input:    `rate(up{job="test"}[$__rate_interval])`,
			matchers: map[string]string{"env": "prod"},
			expected: `rate(up{env="prod",job="test"}[$__rate_interval])`,
		},
		{
			name:     "Metric name variable: ${var} syntax",
			input:    `${metric_name}{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `${metric_name}{env="prod",job="test"}`,
		},
		{
			name:     "Metric name variable: $var syntax",
			input:    `$metric_name{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `$metric_name{env="prod",job="test"}`,
		},
		{
			name:     "Metric name suffix variable: ${suffix_total}",
			input:    `otelcol_receiver_${suffix_total}{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_receiver_${suffix_total}{env="prod",job="test"}`,
		},
		{
			name:     "Metric name suffix variable: $suffix_total",
			input:    `otelcol_receiver_$suffix_total{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_receiver_$suffix_total{env="prod",job="test"}`,
		},
		{
			name:     "Multiple metric suffix variables: ${suffix1} and ${suffix2}",
			input:    `otelcol_process_${suffix1}_uptime_${suffix2}{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_process_${suffix1}_uptime_${suffix2}{env="prod",job="test"}`,
		},
		{
			name:     "Multiple metric suffix variables: $suffix1 and $suffix2",
			input:    `otelcol_process_$suffix1_uptime_$suffix2{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_process_$suffix1_uptime_$suffix2{env="prod",job="test"}`,
		},
		{
			name:     "Complex real-world query",
			input:    `sum(rate(otelcol_receiver_accepted${suffix_total}{receiver=~"$receiver",job="$job"}[$__rate_interval])) by (receiver)`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by (receiver) (rate(otelcol_receiver_accepted${suffix_total}{cluster="prod",job="$job",receiver=~"$receiver"}[$__rate_interval]))`,
		},
		{
			name:     "Grouping variable in by clause",
			input:    `sum(rate(up[5m])) by ($grouping)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping) (rate(up{env="prod"}[5m]))`,
		},
		{
			name:        "Unsupported: variable as prefix in metric name",
			input:       `${prefix}_metric{job="test"}`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "", // Parser will reject this as invalid syntax
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &tool.PromQL{}
			result, err := p.Transform(tt.input, &tt.matchers)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestPromQLTransformEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "No variables",
			input:    `up{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `up{env="prod",job="test"}`,
		},
		{
			name:     "Variable in regex",
			input:    `up{job=~"$job.*"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `up{env="prod",job=~"$job.*"}`,
		},
		{
			name:     "Aggregation parameter",
			input:    `topk($limit, up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `topk($limit, up{env="prod"})`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &tool.PromQL{}
			result, err := p.Transform(tt.input, &tt.matchers)

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPromQLThreeVariableTypes tests the three critical variable positions:
// 1. Variables in metric names (structural)
// 2. Variables in durations (structural)
// 3. Variables in label values (value position)
func TestPromQLThreeVariableTypes(t *testing.T) {
	tests := []struct {
		name        string
		description string
		input       string
		matchers    map[string]string
		expected    string
	}{
		{
			name:        "Type 1: Variable in metric name only",
			description: "Metric name suffix variable should be preserved",
			input:       `otelcol_receiver${suffix_total}{job="test"}`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `otelcol_receiver${suffix_total}{cluster="prod",job="test"}`,
		},
		{
			name:        "Type 2: Variable in duration only",
			description: "Duration variable should be preserved",
			input:       `rate(up{job="test"}[$__rate_interval])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(up{cluster="prod",job="test"}[$__rate_interval])`,
		},
		{
			name:        "Type 3: Variable in labels value only",
			description: "Label value variable should be preserved",
			input:       `up{instance="${instance}", job="$job"}`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `up{cluster="prod",instance="${instance}",job="$job"}`,
		},
		{
			name:        "Types 1+2: Metric name + Duration",
			description: "Both metric name and duration variables should be preserved",
			input:       `rate(otelcol_receiver${suffix_total}{job="test"}[$__rate_interval])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(otelcol_receiver${suffix_total}{cluster="prod",job="test"}[$__rate_interval])`,
		},
		{
			name:        "Types 1+3: Metric name + Label value",
			description: "Both metric name and label value variables should be preserved",
			input:       `otelcol_receiver${suffix_total}{job="$job"}`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `otelcol_receiver${suffix_total}{cluster="prod",job="$job"}`,
		},
		{
			name:        "Types 2+3: Duration + Label value",
			description: "Both duration and label value variables should be preserved",
			input:       `rate(up{job="$job"}[$__rate_interval])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(up{cluster="prod",job="$job"}[$__rate_interval])`,
		},
		{
			name:        "Types 1+2+3: All three types",
			description: "All three variable types should be preserved in a single query",
			input:       `rate(otelcol_receiver${suffix_total}{job="$job"}[$__rate_interval])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(otelcol_receiver${suffix_total}{cluster="prod",job="$job"}[$__rate_interval])`,
		},
		{
			name:        "Types 1+2+3: Complex with multiple metrics",
			description: "All three variable types across multiple metrics",
			input:       `rate(metric${suffix_total}{job="$job"}[$__rate_interval]) + rate(other${suffix_count}{instance="$instance"}[$__rate_interval_ms])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(metric${suffix_total}{cluster="prod",job="$job"}[$__rate_interval]) + rate(other${suffix_count}{cluster="prod",instance="$instance"}[$__rate_interval_ms])`,
		},
		{
			name:        "Types 1+2+3: With aggregation",
			description: "All three types with aggregation function",
			input:       `sum(rate(otelcol_receiver${suffix_total}{receiver=~"$receiver"}[$__rate_interval])) by (receiver)`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `sum by (receiver) (rate(otelcol_receiver${suffix_total}{cluster="prod",receiver=~"$receiver"}[$__rate_interval]))`,
		},
		{
			name:        "Types 1+2+3: Multiple variables per type",
			description: "Multiple variables of each type",
			input:       `rate(metric${v1}_name${v2}{job="$job",env="$env"}[$__interval]) / rate(other${v3}{instance="$instance"}[$__rate_interval])`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `rate(metric${v1}_name${v2}{cluster="prod",env="$env",job="$job"}[$__interval]) / rate(other${v3}{cluster="prod",instance="$instance"}[$__rate_interval])`,
		},
		{
			name:        "Types 1+2+3: Real-world OpenTelemetry example",
			description: "Real query from OpenTelemetry dashboard",
			input:       `sum(rate(otelcol_receiver_accepted${suffix_total}{receiver=~"$receiver",job="$job"}[$__rate_interval])) by (receiver)`,
			matchers:    map[string]string{"cluster": "prod", "namespace": "monitoring"},
			expected:    `sum by (receiver) (rate(otelcol_receiver_accepted${suffix_total}{cluster="prod",job="$job",namespace="monitoring",receiver=~"$receiver"}[$__rate_interval]))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &tool.PromQL{}
			result, err := p.Transform(tt.input, &tt.matchers)

			assert.NoError(t, err, "Transform should not return error")
			assert.Equal(t, tt.expected, result, tt.description)

			// Additional validation: verify all variable types are preserved
			if strings.Contains(tt.input, "${") || strings.Contains(tt.input, "$") {
				// Count variables in input
				inputVarCount := strings.Count(tt.input, "$")
				resultVarCount := strings.Count(result, "$")

				assert.Equal(t, inputVarCount, resultVarCount, "Variable count should be preserved")
			}

			// Verify matchers were injected
			for key, value := range tt.matchers {
				expectedMatcher := key + "=\"" + value + "\""
				assert.Contains(t, result, expectedMatcher, "Matcher should be injected: %s", expectedMatcher)
			}
		})
	}
}

// TestPromQLTransformWithGroupingVariables tests variables in by/without clauses.
// Variables in grouping positions should be preserved through the transform pipeline.
func TestPromQLTransformWithGroupingVariables(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "by with single variable",
			input:    `sum by ($grouping) (up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping) (up{env="prod"})`,
		},
		{
			name:     "without with single variable",
			input:    `sum without ($exclude) (up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum without ($exclude) (up{env="prod"})`,
		},
		{
			name:     "by with ${var} syntax",
			input:    `sum by (${grouping}) (up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by (${grouping}) (up{env="prod"})`,
		},
		{
			name:     "by with variable and fixed labels",
			input:    `sum by ($var, job, instance) (up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($var, job, instance) (up{env="prod"})`,
		},
		{
			name:     "by with multiple variables",
			input:    `sum by ($var1, $var2) (up)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($var1, $var2) (up{env="prod"})`,
		},
		{
			name:     "by variable combined with duration variable",
			input:    `sum by ($grouping) (rate(up[$__rate_interval]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping) (rate(up{env="prod"}[$__rate_interval]))`,
		},
		{
			name:     "by variable combined with label value variable",
			input:    `sum by ($grouping) (up{job="$job"})`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping) (up{env="prod",job="$job"})`,
		},
		{
			name:     "by variable combined with metric name and duration and label variables",
			input:    `sum by ($grouping) (rate(otelcol_receiver${suffix_total}{job="$job"}[$__rate_interval]))`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by ($grouping) (rate(otelcol_receiver${suffix_total}{cluster="prod",job="$job"}[$__rate_interval]))`,
		},
		{
			name:     "nested aggregation with different grouping variables",
			input:    `sum by ($outer) (avg by ($inner) (up))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($outer) (avg by ($inner) (up{env="prod"}))`,
		},
		{
			name:     "topk with by variable",
			input:    `topk(5, sum by ($grouping) (rate(up[5m])))`,
			matchers: map[string]string{"env": "prod"},
			expected: `topk(5, sum by ($grouping) (rate(up{env="prod"}[5m])))`,
		},
		{
			name:     "same variable in grouping and label value",
			input:    `sum by ($job) (up{job="$job"})`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($job) (up{env="prod",job="$job"})`,
		},
		{
			name:     "by clause after aggregation (postfix syntax)",
			input:    `sum(rate(up{job="$job"}[5m])) by ($grouping)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping) (rate(up{env="prod",job="$job"}[5m]))`,
		},
		{
			name:     "by with label and variable without comma (Grafana pattern)",
			input:    `sum(rate(up[5m])) by (receiver $grouping)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by (receiver, $grouping) (rate(up{env="prod"}[5m]))`,
		},
		{
			name:     "by with variable before label without comma",
			input:    `sum(rate(up[5m])) by ($grouping receiver)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by ($grouping, receiver) (rate(up{env="prod"}[5m]))`,
		},
		{
			name:     "without with label and variable without comma",
			input:    `sum(rate(up[5m])) without (instance $exclude)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum without (instance, $exclude) (rate(up{env="prod"}[5m]))`,
		},
		{
			name:     "without with single variable at end of expression",
			input:    `sum(rate(up[5m])) without ($exclude)`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum without ($exclude) (rate(up{env="prod"}[5m]))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &tool.PromQL{}
			result, err := p.Transform(tt.input, &tt.matchers)

			assert.NoError(t, err, "Transform should not return error for grouping variables")
			assert.Equal(t, tt.expected, result)

			// Verify all original variables are preserved
			inputVarCount := strings.Count(tt.input, "$")
			resultVarCount := strings.Count(result, "$")
			assert.Equal(t, inputVarCount, resultVarCount, "All variables should be preserved")

			// Verify matchers were injected
			for key, value := range tt.matchers {
				expectedMatcher := key + `="` + value + `"`
				assert.Contains(t, result, expectedMatcher, "Matcher should be injected: %s", expectedMatcher)
			}
		})
	}
}

// TestPromQLTransformWithFunctionNameVariables tests variables in function name positions.
// These are Grafana custom variables like ${metric:value} that resolve to function names (rate, increase).
func TestPromQLTransformWithFunctionNameVariables(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "simple function name variable",
			input:    `${metric:value}(up{job="test"}[5m])`,
			matchers: map[string]string{"env": "prod"},
			expected: `${metric:value}(up{env="prod",job="test"}[5m])`,
		},
		{
			name:     "function name variable with $var syntax",
			input:    `$func(up{job="test"}[5m])`,
			matchers: map[string]string{"env": "prod"},
			expected: `$func(up{env="prod",job="test"}[5m])`,
		},
		{
			name:     "function name variable with metric suffix variable",
			input:    `${metric:value}(otelcol_receiver${suffix_total}{job="test"}[5m])`,
			matchers: map[string]string{"env": "prod"},
			expected: `${metric:value}(otelcol_receiver${suffix_total}{env="prod",job="test"}[5m])`,
		},
		{
			name:     "function name variable with duration variable",
			input:    `${metric:value}(up{job="test"}[$__rate_interval])`,
			matchers: map[string]string{"env": "prod"},
			expected: `${metric:value}(up{env="prod",job="test"}[$__rate_interval])`,
		},
		{
			name:     "function name variable with label value variable",
			input:    `${metric:value}(up{job="$job"}[5m])`,
			matchers: map[string]string{"env": "prod"},
			expected: `${metric:value}(up{env="prod",job="$job"}[5m])`,
		},
		{
			name:     "function name variable with all other variable types",
			input:    `sum(${metric:value}(otelcol_receiver${suffix_total}{job="$job"}[$__rate_interval])) by (receiver)`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by (receiver) (${metric:value}(otelcol_receiver${suffix_total}{cluster="prod",job="$job"}[$__rate_interval]))`,
		},
		{
			name:     "function name variable with grouping variable",
			input:    `sum(${metric:value}(otelcol_exporter_send_failed_log_records${suffix_total}{exporter=~"$exporter",job="$job"}[$__rate_interval])) by (exporter, $grouping)`,
			matchers: map[string]string{"cluster": "prod", "namespace": "monitoring"},
			expected: `sum by (exporter, $grouping) (${metric:value}(otelcol_exporter_send_failed_log_records${suffix_total}{cluster="prod",exporter=~"$exporter",job="$job",namespace="monitoring"}[$__rate_interval]))`,
		},
		{
			name:     "function variable in nested expression",
			input:    `sum(${metric:value}(up[5m])) / sum(increase(up[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum(${metric:value}(up{env="prod"}[5m])) / sum(increase(up{env="prod"}[5m]))`,
		},
		{
			name:     "multiple function name variables",
			input:    `${func1}(up[5m]) + ${func2}(down[5m])`,
			matchers: map[string]string{"env": "prod"},
			expected: `${func1}(up{env="prod"}[5m]) + ${func2}(down{env="prod"}[5m])`,
		},
		{
			name:     "function variable inside sum with real-world pattern",
			input:    `sum(${metric:value}(otelcol_processor_batch_batch_send_size_count{job="$job"}[$__rate_interval]))`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum(${metric:value}(otelcol_processor_batch_batch_send_size_count{cluster="prod",job="$job"}[$__rate_interval]))`,
		},
		{
			name:     "function variable with grouping without comma (real dashboard pattern)",
			input:    `sum(${metric:value}(otelcol_receiver_accepted_metric_points${suffix_total}{receiver=~"$receiver",job="$job"}[$__rate_interval])) by (receiver $grouping)`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by (receiver, $grouping) (${metric:value}(otelcol_receiver_accepted_metric_points${suffix_total}{cluster="prod",job="$job",receiver=~"$receiver"}[$__rate_interval]))`,
		},
		{
			name:     "function variable with arithmetic prefix 0-sum",
			input:    `0-sum(${metric:value}(otelcol_processor_outgoing_items${suffix_total}{processor=~"$processor",job="$job",otel_signal="logs"}[$__rate_interval])) by (processor $grouping)`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `0 - sum by (processor, $grouping) (${metric:value}(otelcol_processor_outgoing_items${suffix_total}{cluster="prod",job="$job",otel_signal="logs",processor=~"$processor"}[$__rate_interval]))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &tool.PromQL{}
			result, err := p.Transform(tt.input, &tt.matchers)

			assert.NoError(t, err, "Transform should not return error for function name variables")
			assert.Equal(t, tt.expected, result)

			// Verify all original variables are preserved
			inputVarCount := strings.Count(tt.input, "$")
			resultVarCount := strings.Count(result, "$")
			assert.Equal(t, inputVarCount, resultVarCount, "All variables should be preserved")

			// Verify matchers were injected
			for key, value := range tt.matchers {
				expectedMatcher := key + `="` + value + `"`
				assert.Contains(t, result, expectedMatcher, "Matcher should be injected: %s", expectedMatcher)
			}
		})
	}
}

// TestPromQLTransformDashboardPatterns tests patterns found in real Grafana dashboards
// that are not covered by the more targeted test suites above.
func TestPromQLTransformDashboardPatterns(t *testing.T) {
tests := []struct {
name     string
input    string
matchers map[string]string
expected string
}{
{
name: "ratio of two aggregations with grouping without comma",
input: `max(
    otelcol_exporter_queue_size{
        exporter=~"$exporter", job="$job"
    }
) by (exporter $grouping)
/
min(
    otelcol_exporter_queue_capacity{
        exporter=~"$exporter", job="$job"
    }
) by (exporter $grouping)`,
matchers: map[string]string{"cluster": "prod"},
expected: `max by (exporter, $grouping) (otelcol_exporter_queue_size{cluster="prod",exporter=~"$exporter",job="$job"}) / min by (exporter, $grouping) (otelcol_exporter_queue_capacity{cluster="prod",exporter=~"$exporter",job="$job"})`,
},
{
name:     "two consecutive suffix variables in metric name",
input:    `max(otelcol_process_uptime${suffix_seconds}${suffix_total}{service_instance_id=~".*",job="$job"}) by (service_instance_id)`,
matchers: map[string]string{"cluster": "prod"},
expected: `max by (service_instance_id) (otelcol_process_uptime${suffix_seconds}${suffix_total}{cluster="prod",job="$job",service_instance_id=~".*"})`,
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
p := &tool.PromQL{}
result, err := p.Transform(tt.input, &tt.matchers)

assert.NoError(t, err)
assert.Equal(t, tt.expected, result)

for key, value := range tt.matchers {
expectedMatcher := key + `="` + value + `"`
assert.Contains(t, result, expectedMatcher, "Matcher should be injected: %s", expectedMatcher)
}
})
}
}

func TestPromQLFunctionNameVariablePoolExhausted(t *testing.T) {
// When all placeholder functions in the pool are already used by real function calls in the
// expression, Transform must return an error instead of silently producing a wrong result.
p := &tool.PromQL{}
matchers := map[string]string{"env": "prod"}
input := `rate(a[5m]) + irate(b[5m]) + increase(c[5m]) + delta(d[5m]) + changes(e[5m]) + resets(f[5m]) + deriv(g[5m]) + idelta(h[5m]) + ${fn:value}(x[5m])`
_, err := p.Transform(input, &matchers)
assert.Error(t, err, "should error when all placeholder functions are already in use")
assert.Contains(t, err.Error(), "cannot safely replace function name variable")
}
