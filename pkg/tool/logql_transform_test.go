package tool_test

import (
	"strings"
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func TestShouldApplyLabelMatcherToLogQLSelector(t *testing.T) {
	cases := []TestCase{
		{
			Input:    `sum(rate({app="foo", env="production"} |= "error" [5m])) by (job) `,
			Matchers: map[string]string{"bar": "baz"},
			Expected: `sum by(job)(rate({app="foo", env="production", bar="baz"} |= "error"[5m]))`,
		},
		{
			Input:    `rate({filename="test"}[1m])`,
			Matchers: map[string]string{"bar": "baz"},
			Expected: `rate({filename="test", bar="baz"}[1m])`,
		},
		{
			Input:    `{job="loki"} !~ ".+"`,
			Matchers: map[string]string{"model": "cos"},
			Expected: `{job="loki", model="cos"} !~ ".+"`,
		},
		{
			Input:    `{cool="breeze"} |= "weather"`,
			Matchers: map[string]string{"hot": "sunrays", "dance": "macarena"},
			Expected: `{cool="breeze", dance="macarena", hot="sunrays"} |= "weather"`,
		},
	}
	for _, c := range cases {
		p := &tool.LogQL{}
		out, err := p.Transform(c.Input, &c.Matchers)
		assert.NoError(t, err)
		assert.Equal(t, c.Expected, out)
	}
}

func TestLogQLTransformErrorHandling(t *testing.T) {
	p := &tool.LogQL{}

	// Test cases for malformed LogQL expressions that should fail gracefully
	testCases := []struct {
		name        string
		input       string
		matchers    map[string]string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Invalid syntax - unclosed braces",
			input:       `{job="test"`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error", // Expected to fail during parsing
		},
		{
			name:        "Invalid syntax - malformed regex",
			input:       `{job="test"} =~ "["`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error", // Expected to fail during parsing
		},
		{
			name:        "Invalid syntax - incomplete pipeline",
			input:       `rate({job="test"}[5m] |`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error",
		},
		{
			name:        "Invalid syntax - unmatched parentheses",
			input:       `sum(rate({job="test"}[5m]))))`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error",
		},
		{
			name:        "Empty expression",
			input:       "",
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error",
		},
		{
			name:        "Invalid aggregation - missing metric",
			input:       `sum by(job)() > 0`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "syntax error",
		},
		{
			name:        "Invalid range vector - negative duration",
			input:       `rate({job="test"}[-5m])`,
			matchers:    map[string]string{"env": "prod"},
			expectError: true,
			errorMsg:    "not a valid duration string",
		},
		{
			name:        "Complex valid expression (should succeed)",
			input:       `sum by(job) (rate({filename="/var/log/app.log", level="error"}[5m])) > 10`,
			matchers:    map[string]string{"model": "production", "region": "us-west"},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := p.Transform(tc.input, &tc.matchers)

			if tc.expectError {
				assert.Error(t, err, "Expected error for input: %s", tc.input)
				assert.Contains(t, err.Error(), tc.errorMsg,
					"Expected error message to contain '%s' for input: %s", tc.errorMsg, tc.input)
				// When error occurs, result should be the original input (fallback behavior)
				assert.Equal(t, tc.input, result,
					"Result should be original input when transformation fails")
			} else {
				assert.NoError(t, err, "Unexpected error for valid input: %s", tc.input)
				assert.NotEmpty(t, result, "Result should not be empty for valid transformation")
				// Verify that matchers were actually injected
				for key, value := range tc.matchers {
					assert.Contains(t, result, key, "Expected matcher '%s' to be injected", key)
					assert.Contains(t, result, value, "Expected matcher value '%s' to be present", value)
				}
			}
		})
	}
}

func TestLogQLTransformWithEmptyMatchers(t *testing.T) {
	p := &tool.LogQL{}

	// Test with empty matchers map
	emptyMatchers := map[string]string{}
	result, err := p.Transform(`{job="test"}`, &emptyMatchers)
	assert.NoError(t, err, "Should not error with empty matchers")
	assert.Equal(t, `{job="test"}`, result, "Should return original expression with empty matchers")
}

func TestLogQLTransformDoesNotPanicWithValidInputs(t *testing.T) {
	p := &tool.LogQL{}

	// Test various valid inputs to ensure no panics
	testCases := []struct {
		name     string
		input    string
		matchers map[string]string
	}{
		{
			name:     "Simple selector",
			input:    `{job="test"}`,
			matchers: map[string]string{"env": "prod"},
		},
		{
			name:     "Complex expression with aggregation",
			input:    `sum by(job) (rate({job="test"}[5m])) > 10`,
			matchers: map[string]string{"model": "production"},
		},
		{
			name:     "Expression with regex filter",
			input:    `{job="test"} |~ "pattern.*"`,
			matchers: map[string]string{"region": "us-west"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// This should not panic
			assert.NotPanics(t, func() {
				result, err := p.Transform(tc.input, &tc.matchers)
				assert.NoError(t, err, "Should not error for valid input")
				assert.NotEmpty(t, result, "Result should not be empty")
			})
		})
	}
}

func TestLogQLTransformSpecialCharactersInMatchers(t *testing.T) {
	p := &tool.LogQL{}

	// Test with special characters that might cause parsing issues
	matchers := map[string]string{
		"special-chars": "test@#$%^&*()",
		"unicode":       "héllo-wörld",
		"quotes":        `"quoted string"`,
		"spaces":        "value with spaces",
	}

	result, err := p.Transform(`{job="test"}`, &matchers)
	assert.NoError(t, err, "Should handle special characters in matchers")
	assert.NotEmpty(t, result, "Result should not be empty")

	// Verify all matchers are present in result (properly escaped)
	for key := range matchers {
		assert.Contains(t, result, key, "Matcher key '%s' should be present", key)
	}
}

func TestLogQLTransformWithGrafanaVariables(t *testing.T) {
	cases := []TestCase{
		{
			Input:    `sum by(filename) (sum_over_time({filename="/var/log/app.log"} | json | timestamp >= ${__from} | unwrap metric [5y]))`,
			Matchers: map[string]string{"juju_model": "cos"},
			Expected: `sum by(filename)(sum_over_time({filename="/var/log/app.log", juju_model="cos"} | json | timestamp>=${__from} | unwrap metric[5y]))`,
		},
		{
			Input:    `rate({app="foo"} | timestamp >= ${__from} and timestamp <= ${__to} [5m])`,
			Matchers: map[string]string{"env": "prod"},
			Expected: `rate({app="foo", env="prod"} | ( timestamp>=${__from} , timestamp<=${__to} )[5m])`,
		},
		{
			Input:    `{job="test"} | duration > $__interval_ms`,
			Matchers: map[string]string{"region": "us"},
			Expected: `{job="test", region="us"} | duration>$__interval_ms`,
		},
		{
			Input:    `sum(rate({app=~"$app"} | value >= ${__from} [5m])) by (instance)`,
			Matchers: map[string]string{"cluster": "prod"},
			Expected: `sum by(instance)(rate({app=~"$app", cluster="prod"} | value>=${__from}[5m]))`,
		},
		{
			Input:    `{filename="/var/log/test.log"} | json | timestamp >= ${__from} | timestamp <= ${__to}`,
			Matchers: map[string]string{"env": "staging"},
			Expected: `{filename="/var/log/test.log", env="staging"} | json | timestamp>=${__from} | timestamp<=${__to}`,
		},
		{
			Input:    `{app="myapp"} | duration >= ${__range_ms}`,
			Matchers: map[string]string{"namespace": "prod"},
			Expected: `{app="myapp", namespace="prod"} | duration>=${__range_ms}`,
		},
	}
	for _, c := range cases {
		p := &tool.LogQL{}
		out, err := p.Transform(c.Input, &c.Matchers)
		assert.NoError(t, err)
		assert.Equal(t, c.Expected, out)
	}
}

func TestGrafanaVariableReplacement(t *testing.T) {
	// Test the internal helper functions
	testCases := []struct {
		name     string
		input    string
		expected int // number of variables expected
	}{
		{
			name:     "Single variable ${__from}",
			input:    `timestamp >= ${__from}`,
			expected: 1,
		},
		{
			name:     "Multiple variables",
			input:    `timestamp >= ${__from} and timestamp <= ${__to}`,
			expected: 2,
		},
		{
			name:     "Short form variable",
			input:    `duration > $__interval_ms`,
			expected: 1,
		},
		{
			name:     "Variable with format option",
			input:    `time >= ${__from:date}`,
			expected: 1,
		},
		{
			name:     "Multiple same variables",
			input:    `value >= ${__from} or value2 >= ${__from}`,
			expected: 2,
		},
		{
			name:     "Three different variables",
			input:    `timestamp >= ${__from} and timestamp <= ${__to} and interval = ${__interval}`,
			expected: 3,
		},
		{
			name:     "No variables",
			input:    `{job="test"} | rate [5m]`,
			expected: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processed, occurrences := tool.ReplaceGrafanaVariables(tc.input)

			// Check we found the right number of variables
			assert.Equal(t, tc.expected, len(occurrences), "Expected %d variable occurrences", tc.expected)

			// If variables were found, verify they were replaced with numbers
			if tc.expected > 0 {
				assert.NotEqual(t, tc.input, processed, "Query should be modified")
				assert.NotContains(t, processed, "${", "Processed query should not contain ${")
				assert.NotContains(t, processed, "$__", "Processed query should not contain $__")

				// Verify we can restore the original
				restored := tool.RestoreGrafanaVariables(processed, occurrences)
				assert.Equal(t, tc.input, restored, "Restored query should match original")
			} else {
				assert.Equal(t, tc.input, processed, "Query without variables should not be modified")
			}
		})
	}
}

func TestGrafanaVariableEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		matchers map[string]string
		wantErr  bool
	}{
		{
			name:     "Variable in filter expression",
			input:    `{job="test"} | timestamp >= ${__from}`,
			matchers: map[string]string{"app": "test"},
			wantErr:  false,
		},
		{
			name:     "Variable at end of query",
			input:    `{job="test"} | value > ${__to}`,
			matchers: map[string]string{"env": "prod"},
			wantErr:  false,
		},
		{
			name:     "Complex query with multiple variables",
			input:    `rate({job="test"} | value >= ${__from} and value <= ${__to} and duration > $__interval_ms [5m])`,
			matchers: map[string]string{"namespace": "prod"},
			wantErr:  false,
		},
		{
			name:     "Variable in label value (custom variable)",
			input:    `{job=~"$job_var"} | timestamp >= ${__from}`,
			matchers: map[string]string{"cluster": "prod"},
			wantErr:  false,
		},
		{
			name:     "Multiple occurrences of same variable",
			input:    `{job="test"} | value1 >= ${__from} or value2 >= ${__from}`,
			matchers: map[string]string{"region": "us"},
			wantErr:  false,
		},
		{
			name:     "Variable with format option",
			input:    `{job="test"} | timestamp >= ${__from:date}`,
			matchers: map[string]string{"zone": "east"},
			wantErr:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := &tool.LogQL{}
			result, err := p.Transform(tc.input, &tc.matchers)

			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result)

				// Verify matchers were injected
				for key, value := range tc.matchers {
					assert.Contains(t, result, key)
					assert.Contains(t, result, value)
				}

				// Verify variables are still present in output
				if strings.Contains(tc.input, "${__") {
					assert.Contains(t, result, "${__", "Output should preserve Grafana variables")
				}
				if strings.Contains(tc.input, "$__") && !strings.Contains(tc.input, "${__") {
					assert.Contains(t, result, "$__", "Output should preserve short-form Grafana variables")
				}
			}
		})
	}
}
