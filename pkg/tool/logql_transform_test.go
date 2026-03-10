package tool_test

import (
	"regexp"
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

	// Test cases for malformed LogQL expressions
	// Verifies that invalid syntax returns errors without panicking
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

func TestLogQLTransformDoesNotDuplicateExistingLabel(t *testing.T) {
	p := &tool.LogQL{}
	// When the label being injected already exists in the stream selector, it must not be duplicated.
	matchers := map[string]string{"env": "prod"}
	result, err := p.Transform(`rate({job="test", env="existing"}[5m])`, &matchers)
	assert.NoError(t, err)
	assert.Equal(t, `rate({job="test", env="existing"}[5m])`, result)
	assert.NotContains(t, result, `env="prod"`, "label already in selector must not be overwritten")
}

func TestLogQLGroupingVariableReusedAcrossClauses(t *testing.T) {
	p := &tool.LogQL{}
	// Same grouping variable in two separate by() clauses — hits the placeholder cache path.
	matchers := map[string]string{"cluster": "prod"}
	input := `sum by ($grouping) (rate({app="svc1"}[5m])) / sum by ($grouping) (rate({app="svc2"}[5m]))`
	result, err := p.Transform(input, &matchers)
	assert.NoError(t, err)
	assert.Contains(t, result, `$grouping`, "grouping variable must be preserved in both clauses")
	assert.Equal(t, 2, strings.Count(result, "$grouping"), "grouping variable must appear twice")
	assert.Contains(t, result, `cluster="prod"`)
}

func TestLogQLTransformWithEmptyMatchers(t *testing.T) {
	p := &tool.LogQL{}

	emptyMatchers := map[string]string{}
	result, err := p.Transform(`{job="test"}`, &emptyMatchers)
	assert.NoError(t, err, "Should not error with empty matchers")
	assert.Equal(t, `{job="test"}`, result, "Should return original expression with empty matchers")
}

func TestLogQLTransformDoesNotPanicWithValidInputs(t *testing.T) {
	p := &tool.LogQL{}

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

func TestLogQLTransformWithGrouping(t *testing.T) {
	p := &tool.LogQL{}

	testCases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "Sum aggregation with by clause - single grouping label",
			input:    `sum by(job) (rate({app="foo"}[$__rate_interval]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by(job)(rate({app="foo", env="prod"}[$__rate_interval]))`,
		},
		{
			name:     "Sum aggregation with by clause - multiple grouping labels",
			input:    `sum by(job, instance, region) (rate({app=${app}}[5m]))`,
			matchers: map[string]string{"env": "prod", "cluster": "main"},
			expected: `sum by(job,instance,region)(rate({app=${app}, cluster="main", env="prod"}[5m]))`,
		},
		{
			name:     "Count aggregation with without clause - single excluded label",
			input:    `count without(level) (rate({app="bar"}[$_rate_interval]))`,
			matchers: map[string]string{"model": "test"},
			expected: `count without(level)(rate({app="bar", model="test"}[$_rate_interval]))`,
		},
		{
			name:     "Count aggregation with without clause - multiple excluded labels",
			input:    `count without(level, host) (rate({app="bar"}[1m]))`,
			matchers: map[string]string{"model": "test", "region": "us"},
			expected: `count without(level,host)(rate({app="bar", model="test", region="us"}[1m]))`,
		},
		{
			name:     "Avg aggregation with by clause and line filters",
			input:    `avg by(namespace) (rate({job="app"} |= "error" [10m]))`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `avg by(namespace)(rate({job="app", cluster="prod"} |= "error"[10m]))`,
		},
		{
			name:     "Max aggregation with without clause and multiple line filters",
			input:    `max without(pod) (rate({service="api"} |~ ".*ERROR.*" != "timeout" [5m]))`,
			matchers: map[string]string{"env": "staging"},
			expected: `max without(pod)(rate({service="api", env="staging"} |~ ".*ERROR.*" != "timeout"[5m]))`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := p.Transform(tc.input, &tc.matchers)
			assert.NoError(t, err, "Should not error for valid LogQL with grouping")
			assert.Equal(t, tc.expected, result, "Transformed query should match expected output")
		})
	}
}

// Tests transformation with variables in label values
// Common pattern in Grafana where label values are parameterized with dashboard variables
func TestLogQLTransformWithVariablesInLabelValues(t *testing.T) {
	p := &tool.LogQL{}

	testCases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "single variable in label value",
			input:    `{app="$application"}`,
			matchers: map[string]string{"env": "prod"},
			expected: `{app="$application", env="prod"}`,
		},
		{
			name:     "multiple variables in different labels",
			input:    `{app="$app", namespace="$namespace"}`,
			matchers: map[string]string{"cluster": "main"},
			expected: `{app="$app", namespace="$namespace", cluster="main"}`,
		},
		{
			name:     "variable with curly braces in label value",
			input:    `{job="${job_name}", instance="${instance}"}`,
			matchers: map[string]string{"region": "us-east"},
			expected: `{job="${job_name}", instance="${instance}", region="us-east"}`,
		},
		{
			name:     "mixed: variable and fixed values",
			input:    `{app="$app", env="production"}`,
			matchers: map[string]string{"team": "platform"},
			expected: `{app="$app", env="production", team="platform"}`,
		},
		{
			name:     "variable in label value with rate and by clause",
			input:    `sum by(job) (rate({service="$service"}[5m]))`,
			matchers: map[string]string{"env": "staging"},
			expected: `sum by(job)(rate({service="$service", env="staging"}[5m]))`,
		},
		{
			name:     "variable with regex matcher",
			input:    `{app=~"$app_regex"}`,
			matchers: map[string]string{"namespace": "default"},
			expected: `{app=~"$app_regex", namespace="default"}`,
		},
		{
			name:     "variable in label value with log filters",
			input:    `{app="$app"} |= "error" | json`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `{app="$app", cluster="prod"} |= "error" | json`,
		},
		{
			name:     "multiple variables with grouping and filters",
			input:    `count by(level) (rate({job="$job", namespace="$ns"} |~ "ERROR" [5m]))`,
			matchers: map[string]string{"env": "prod", "region": "eu"},
			expected: `count by(level)(rate({job="$job", namespace="$ns", env="prod", region="eu"} |~ "ERROR"[5m]))`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := p.Transform(tc.input, &tc.matchers)
			assert.NoError(t, err, "Should not error for LogQL with variables in label values")
			assert.Equal(t, tc.expected, result, "Transformed query should preserve variables in label values")
		})
	}
}

// Tests transformation with Grafana built-in variables like ${__from}, ${__to}, $__interval_ms, $__range_ms
// These are time-range and interval variables commonly used in Grafana dashboards
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

// Tests that Grafana variables ($var, ${var}, ${var:format}) are correctly preserved
// through the transformation process, while label matchers are still injected
func TestGrafanaVariableReplacement(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected int // number of variables expected
	}{
		{
			name:     "Single variable in label value",
			input:    `{job="$job"}`,
			expected: 1,
		},
		{
			name:     "Multiple variables in labels",
			input:    `{app="$app", region="$region"}`,
			expected: 2,
		},
		{
			name:     "Variable with braces format",
			input:    `{job="${job}"}`,
			expected: 1,
		},
		{
			name:     "Variable with format option",
			input:    `{job="${job:csv}"}`,
			expected: 1,
		},
		{
			name:     "Multiple same variables",
			input:    `{app="$app", backup_app="$app"}`,
			expected: 2,
		},
		{
			name:     "Three different variables",
			input:    `{app="$app", region="$region", env="$env"}`,
			expected: 3,
		},
		{
			name:     "Variable in filter",
			input:    `{job="test"} |= "$search"`,
			expected: 1,
		},
		{
			name:     "Variable in duration",
			input:    `rate({job="test"}[$__interval])`,
			expected: 1,
		},
		{
			name:     "Custom variable in regex matcher",
			input:    `{job=~"$job_pattern"}`,
			expected: 1,
		},
		{
			name:     "No variables",
			input:    `{job="test"}`,
			expected: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Use Transform API (public API) instead of internal functions
			logql := &tool.LogQL{}
			matchers := make(map[string]string)
			matchers["injected"] = "value"

			result, err := logql.Transform(tc.input, &matchers)

			// Verify transformation succeeds
			assert.NoError(t, err, "Transform should not error")

			// If variables exist, verify they are preserved in output
			if tc.expected > 0 {
				// Variables should be preserved in the final result
				varPattern := regexp.MustCompile(`\$\{[^}]+\}|\$\w+`)
				inputVars := varPattern.FindAllString(tc.input, -1)
				outputVars := varPattern.FindAllString(result, -1)

				// All original variables should be present in output
				assert.Equal(t, len(inputVars), len(outputVars),
					"All variables should be preserved in output")

				// Label matchers should be injected
				assert.Contains(t, result, "injected=\"value\"",
					"Label matcher should be injected")
			} else {
				// No variables, so query might be unchanged except for label injection
				if strings.Contains(tc.input, "{") {
					assert.Contains(t, result, "injected=\"value\"",
						"Label matcher should be injected even without variables")
				}
			}
		})
	}
}

func TestGrafanaVariableEdgeCases(t *testing.T) {
	testCases := []struct {
		name         string
		input        string
		matchers     map[string]string
		wantErr      bool
		wantContains []string // extra substrings the output must contain
	}{
		// Basic variable placement tests
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
			name:     "Variable adjacent to special chars (no space)",
			input:    `{job="test"} | json | value>${__from}`,
			matchers: map[string]string{"app": "test"},
			wantErr:  false,
		},

		// Multiple variables tests
		{
			name:     "Complex query with multiple different variables",
			input:    `rate({job="test"} | value >= ${__from} and value <= ${__to} and duration > $__interval_ms [5m])`,
			matchers: map[string]string{"namespace": "prod"},
			wantErr:  false,
		},
		{
			name:     "Multiple occurrences of same variable",
			input:    `{job="test"} | value1 >= ${__from} or value2 >= ${__from}`,
			matchers: map[string]string{"region": "us"},
			wantErr:  false,
		},
		{
			name:     "Same variable in different formats",
			input:    `{job="test"} | timestamp >= $__from and time <= ${__from}`,
			matchers: map[string]string{"cluster": "main"},
			wantErr:  false,
		},

		// Custom user variables
		{
			name:     "Custom variable in label value",
			input:    `{job=~"$job_var"} | timestamp >= ${__from}`,
			matchers: map[string]string{"cluster": "prod"},
			wantErr:  false,
		},
		{
			name:     "Custom variables with underscores and numbers",
			input:    `{app="$app_name_v2", version="$version_123"}`,
			matchers: map[string]string{"env": "staging"},
			wantErr:  false,
		},

		// Format specifiers
		{
			name:     "Variable with simple format option",
			input:    `{job="test"} | timestamp >= ${__from:date}`,
			matchers: map[string]string{"zone": "east"},
			wantErr:  false,
		},
		{
			name:     "Variable with complex format specifiers",
			input:    `{job="test"} | timestamp >= ${__from:date:iso} and time <= ${__to:date:YYYY-MM-DD}`,
			matchers: map[string]string{"service": "api"},
			wantErr:  false,
		},

		// Complex nested scenarios
		{
			name:     "Nested braces with variables in json expressions",
			input:    `{job="test"} | json data="${response}" | data_extracted="${data.field.$var_name}"`,
			matchers: map[string]string{"app": "parser"},
			wantErr:  false,
		},

		// Variables in structural positions (not supported - these cases should fail during parsing)
		{
			name:     "Variable in unwrap (structural position)",
			input:    `{job="test"} | unwrap $metric_name`,
			matchers: map[string]string{"env": "prod"},
			wantErr:  true,
		},
		{
			name:     "Variable in aggregation by clause",
			input:    `sum by($group_by) (rate({job="test"}[5m]))`,
			matchers: map[string]string{"namespace": "kube"},
			wantErr:  false,
			wantContains: []string{
				`namespace="kube"`, // injected matcher must be present
				`$group_by`,        // grouping variable must be preserved
			},
		},
		{
			name:     "Variable in duration range (structural position)",
			input:    `{job="test"} [${__range_s}s]`,
			matchers: map[string]string{"app": "metrics"},
			wantErr:  true,
		},
		{
			name:     "Variable as function name (structural position)",
			input:    `$metric_function({job="test"}[5m])`,
			matchers: map[string]string{"type": "aggregation"},
			wantErr:  true,
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

				for _, s := range tc.wantContains {
					assert.Contains(t, result, s)
				}

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

func TestGrafanaVariablesInQuotedStrings(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		matchers map[string]string
		wantErr  bool
	}{
		{
			name:     "Variable in line_format string",
			input:    `{job="test"} | line_format "User: $username logged in"`,
			matchers: map[string]string{"app": "myapp"},
			wantErr:  false,
		},
		{
			name:     "Multiple variables in line_format",
			input:    `{job="test"} | line_format "Time: ${timestamp}, User: $user, Action: $action"`,
			matchers: map[string]string{"env": "prod"},
			wantErr:  false,
		},
		{
			name:     "Variable in label_format",
			input:    `{job="test"} | label_format new_label="prefix-$old_label-suffix"`,
			matchers: map[string]string{"cluster": "k8s"},
			wantErr:  false,
		},
		{
			name:     "Mixed: variables in quotes and outside",
			input:    `{app="$app_name"} | line_format "Value: $value" | timestamp >= ${__from}`,
			matchers: map[string]string{"namespace": "default"},
			wantErr:  false,
		},
		{
			name:     "Variable in regex pattern within quotes",
			input:    `{job="test"} |~ "error.*$error_type.*failed"`,
			matchers: map[string]string{"severity": "high"},
			wantErr:  false,
		},
		{
			name:     "Backtick strings with variables",
			input:    "{job=\"test\"} | line_format `Status: $status at ${time}`",
			matchers: map[string]string{"region": "us-east"},
			wantErr:  false,
		},
		{
			name:     "Escaped quotes with variables",
			input:    `{job="test"} | line_format "Message: \"$msg\""`,
			matchers: map[string]string{"app": "web"},
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

				// Verify that all variables are preserved in the output
				originalVars := regexp.MustCompile(`\$\{[^}]+\}|\$\w+`).FindAllString(tc.input, -1)
				resultVars := regexp.MustCompile(`\$\{[^}]+\}|\$\w+`).FindAllString(result, -1)

				// All original variables should be in the result
				assert.Equal(t, len(originalVars), len(resultVars),
					"Number of variables should be preserved. Original: %v, Result: %v",
					originalVars, resultVars)
			}
		})
	}
}

// TestLogQLTransformWithGroupingVariables tests variables in by/without clauses.
// Variables in grouping positions should be preserved through the LogQL transform pipeline.
func TestLogQLTransformWithGroupingVariables(t *testing.T) {
	p := &tool.LogQL{}

	tests := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "by with single variable",
			input:    `sum by ($grouping) (rate({job="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({job="test", env="prod"}[5m]))`,
		},
		{
			name:     "without with single variable",
			input:    `sum without ($exclude) (rate({job="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum without($exclude)(rate({job="test", env="prod"}[5m]))`,
		},
		{
			name:     "by with ${var} syntax",
			input:    `sum by (${grouping}) (rate({job="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by(${grouping})(rate({job="test", env="prod"}[5m]))`,
		},
		{
			name:     "by with variable and fixed labels",
			input:    `sum by ($var, job) (rate({app="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($var,job)(rate({app="test", env="prod"}[5m]))`,
		},
		{
			name:     "by with multiple variables",
			input:    `sum by ($var1, $var2) (rate({job="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($var1,$var2)(rate({job="test", env="prod"}[5m]))`,
		},
		{
			name:     "by variable combined with duration variable",
			input:    `sum by ($grouping) (rate({job="test"}[$__rate_interval]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({job="test", env="prod"}[$__rate_interval]))`,
		},
		{
			name:     "by variable combined with label value variable",
			input:    `sum by ($grouping) (rate({job="$job"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({job="$job", env="prod"}[5m]))`,
		},
		{
			name:     "by variable combined with all other variable types",
			input:    `sum by ($grouping) (rate({job="$job"}[$__rate_interval]))`,
			matchers: map[string]string{"cluster": "prod"},
			expected: `sum by($grouping)(rate({job="$job", cluster="prod"}[$__rate_interval]))`,
		},
		{
			name:     "same variable in grouping and label value",
			input:    `sum by ($job) (rate({job="$job"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($job)(rate({job="$job", env="prod"}[5m]))`,
		},
		{
			name:     "by variable with line filters",
			input:    `sum by ($grouping) (rate({job="test"} |= "error" [5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({job="test", env="prod"} |= "error"[5m]))`,
		},
		{
			name:     "by with label and variable without comma (Grafana pattern)",
			input:    `sum by (job $grouping) (rate({app="test"}[5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by(job,$grouping)(rate({app="test", env="prod"}[5m]))`,
		},
		{
			name:     "by pattern inside string literal must not be modified",
			input:    `sum by ($grouping) (rate({job="test"} |= "queued by ($queue $priority)" [5m]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({job="test", env="prod"} |= "queued by ($queue $priority)"[5m]))`,
		},
		{
			// Regression: backtick filter containing "by ($var1 $var2)" must not have its
			// content rewritten. Without backtick masking, normalizeGroupingContent would
			// insert a comma: "by ($queue $priority)" → "by ($queue, $priority)".
			// Note: the LogQL parser canonicalises backtick strings to double-quoted output.
			name:     "by pattern inside backtick filter must not be modified",
			input:    "{app=\"foo\"} |= `queued by ($queue $priority)`",
			matchers: map[string]string{"env": "prod"},
			expected: `{app="foo", env="prod"} |= "queued by ($queue $priority)"`,
		},
		{
			// Regression: backtick template in line_format containing "by ($var1 $var2)"
			// must not have commas inserted. The real by($grouping) clause must still work.
			name:     "by pattern inside backtick line_format template must not be modified",
			input:    "sum by ($grouping) (rate({app=\"foo\"} | line_format `{{.level}} by ($grouping $extra)` [5m]))",
			matchers: map[string]string{"env": "prod"},
			expected: `sum by($grouping)(rate({app="foo", env="prod"} | line_format "{{.level}} by ($grouping $extra)"[5m]))`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.Transform(tt.input, &tt.matchers)

			assert.NoError(t, err, "Transform should not return error for grouping variables")
			assert.Equal(t, tt.expected, result)

			// Verify all original variables are preserved
			varPattern := regexp.MustCompile(`\$\{[^}]+\}|\$\w+`)
			inputVars := varPattern.FindAllString(tt.input, -1)
			resultVars := varPattern.FindAllString(result, -1)
			assert.Equal(t, len(inputVars), len(resultVars), "All variables should be preserved")

			// Verify matchers were injected as key="value" pairs
			for key, value := range tt.matchers {
				expectedMatcher := key + `="` + value + `"`
				assert.Contains(t, result, expectedMatcher, "Matcher should be injected: %s", expectedMatcher)
			}
		})
	}
}

func TestLogQLLineFilterOr(t *testing.T) {
	p := &tool.LogQL{}
	cases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "or between two string values on same filter type",
			input:    `{app="foo"} |= "level=error" or "panic:"`,
			matchers: map[string]string{"env": "prod"},
			expected: `{app="foo", env="prod"} |= "level=error" or |= "panic:"`,
		},
		{
			name:     "or filter followed by pipeline stage",
			input:    `{app="foo"} |= "level=error" or "panic:" | logfmt`,
			matchers: map[string]string{"env": "prod"},
			expected: `{app="foo", env="prod"} |= "level=error" or |= "panic:" | logfmt`,
		},
		{
			name:     "or filter with negation operator",
			input:    `{app="foo"} != "debug" or "trace"`,
			matchers: map[string]string{"env": "prod"},
			expected: `{app="foo", env="prod"} != "debug" or != "trace"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Transform(c.input, &c.matchers)
			assert.NoError(t, err)
			assert.Equal(t, c.expected, result)
		})
	}
}

func TestLogQLSortFunctions(t *testing.T) {
	p := &tool.LogQL{}
	cases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "sort function",
			input:    `sort(sum by (level) (count_over_time({app="foo"}[5m])))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sort(sum by(level)(count_over_time({app="foo", env="prod"}[5m])))`,
		},
		{
			name:     "sort_desc function",
			input:    `sort_desc(topk(10, sum by (job) (count_over_time({app="foo"}[5m]))))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sort_desc(topk(10,sum by(job)(count_over_time({app="foo", env="prod"}[5m]))))`,
		},
		{
			name:     "sort_desc with label matcher injection",
			input:    `sort_desc(sum by (job) (count_over_time({app="foo"}[5m])))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sort_desc(sum by(job)(count_over_time({app="foo", env="prod"}[5m])))`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Transform(c.input, &c.matchers)
			assert.NoError(t, err)
			assert.Equal(t, c.expected, result)
		})
	}
}

// Tests that $__auto (Grafana's automatic interval variable) is preserved through
// transformation, both in plain log queries and wrapped in range aggregations.
// Sourced from real-world loki-bloom-compactor and loki-operational dashboards.
func TestLogQLAutoInterval(t *testing.T) {
	p := &tool.LogQL{}
	cases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "$__auto in count_over_time with line filter",
			input:    `count_over_time({container="bloom-compactor"} |= "level=error" [$__auto])`,
			matchers: map[string]string{"env": "prod"},
			expected: `count_over_time({container="bloom-compactor", env="prod"} |= "level=error"[$__auto])`,
		},
		{
			name:     "$__auto in rate with logfmt parser and label filter",
			input:    `sum(rate({job="distributor"} | logfmt | level="error"[$__auto]))`,
			matchers: map[string]string{"env": "prod"},
			expected: `sum(rate({job="distributor", env="prod"} | logfmt | level="error"[$__auto]))`,
		},
		{
			name:     "$__auto with logfmt parser and Grafana variables in stream selector",
			input:    `sum(rate({cluster="$cluster", namespace="$namespace"} | logfmt | level="error"[$__auto]))`,
			matchers: map[string]string{"juju_model": "test"},
			expected: `sum(rate({cluster="$cluster", namespace="$namespace", juju_model="test"} | logfmt | level="error"[$__auto]))`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Transform(c.input, &c.matchers)
			assert.NoError(t, err)
			assert.Equal(t, c.expected, result)
		})
	}
}

// Tests that backtick strings in label filter comparisons are canonicalised to
// double-quoted strings by the parser. Sourced from real-world nrpe dashboard.
func TestLogQLBacktickLabelFilters(t *testing.T) {
	p := &tool.LogQL{}
	cases := []struct {
		name     string
		input    string
		matchers map[string]string
		expected string
	}{
		{
			name:     "backtick string in equality and regex label filters",
			input:    "{juju_unit=~\"$juju_unit\"} | json | level = `info` | command =~ `.+`",
			matchers: map[string]string{"juju_model": "test"},
			expected: `{juju_unit=~"$juju_unit", juju_model="test"} | json | level="info" | command=~".+"`,
		},
		{
			name:     "backtick string in not-equal label filter",
			input:    "{juju_unit=\"$juju_unit\"} | json | level = `info` | command =~ `.+` | return_code != `0`",
			matchers: map[string]string{"juju_model": "test"},
			expected: `{juju_unit="$juju_unit", juju_model="test"} | json | level="info" | command=~".+" | return_code!="0"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := p.Transform(c.input, &c.matchers)
			assert.NoError(t, err)
			assert.Equal(t, c.expected, result)
		})
	}
}
