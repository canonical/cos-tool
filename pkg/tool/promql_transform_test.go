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
			name:     "Metric name suffix variable",
			input:    `otelcol_receiver${suffix_total}{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_receiver${suffix_total}{env="prod",job="test"}`,
		},
		{
			name:     "Multiple metric suffix variables",
			input:    `otelcol_process${suffix1}_uptime${suffix2}{job="test"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `otelcol_process${suffix1}_uptime${suffix2}{env="prod",job="test"}`,
		},
		{
			name:     "Complex real-world query",
			input:    `sum(rate(otelcol_receiver_accepted${suffix_total}{receiver=~"$receiver",job="$job"}[$__rate_interval])) by (receiver)`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by (receiver) (rate(otelcol_receiver_accepted${suffix_total}{cluster="prod",job="$job",receiver=~"$receiver"}[$__rate_interval]))`,
		},
		{
			name:        "Unsupported: function name variable",
			input:       `${metric:value}(up{job="test"}[5m])`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "function name positions are not supported",
		},
		{
			name:        "Unsupported: grouping variable",
			input:       `sum(rate(up[5m])) by ($grouping)`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "grouping (by/without) positions are not supported",
		},
		{
			name:        "Unsupported: variable at start of metric name",
			input:       `${prefix}_metric{job="test"}`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "", // Can be either our check or parser error
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
			name:        "Type 3: Variable in label value only",
			description: "Label value variable should be preserved",
			input:       `up{job="$job"}`,
			matchers:    map[string]string{"cluster": "prod"},
			expected:    `up{cluster="prod",job="$job"}`,
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
