package tool_test

import (
	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
	"testing"
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
			Matchers: map[string]string{"model": "lma"},
			Expected: `{job="loki", model="lma"} !~ ".+"`,
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
