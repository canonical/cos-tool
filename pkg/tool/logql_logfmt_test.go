package tool_test

import (
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func TestLogQLTransformWithLogfmtExpressions(t *testing.T) {
	matchers := map[string]string{"juju_model": "cos"}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single field",
			input:    `{app="foo"} | logfmt bar`,
			expected: `{app="foo", juju_model="cos"} | logfmt bar="bar"`,
		},
		{
			name:     "field list",
			input:    `{app="foo"} | logfmt field1, field2`,
			expected: `{app="foo", juju_model="cos"} | logfmt field1="field1",field2="field2"`,
		},
		{
			name:     "rename",
			input:    `{app="foo"} | logfmt renamed="field"`,
			expected: `{app="foo", juju_model="cos"} | logfmt renamed="field"`,
		},
		{
			name:     "fields and renames mixed, followed by a label filter",
			input:    `{app="foo"} | logfmt renamed="field", other | other="x"`,
			expected: `{app="foo", juju_model="cos"} | logfmt renamed="field",other="other" | other="x"`,
		},
		{
			name: "field list inside a range aggregation with unwrap",
			input: `quantile_over_time(0.95,
  {pebble_service="mediawiki"}
  | logfmt request_duration_microseconds, path
  | path != "/server-status"
  | unwrap request_duration_microseconds
  [5m]
)`,
			expected: `quantile_over_time(0.95,{pebble_service="mediawiki", juju_model="cos"} | logfmt request_duration_microseconds="request_duration_microseconds",path="path" | path!="/server-status" | unwrap request_duration_microseconds[5m])`,
		},
		{
			name:     "logfmt without fields is unchanged",
			input:    `{app="foo"} | logfmt | level="error"`,
			expected: `{app="foo", juju_model="cos"} | logfmt | level="error"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := &tool.LogQL{}
			out, err := p.Transform(test.input, &matchers)
			assert.NoError(t, err)
			assert.Equal(t, test.expected, out)
		})
	}
}

func TestLogQLTransformWithInvalidLogfmtExpressions(t *testing.T) {
	matchers := map[string]string{"juju_model": "cos"}

	for _, input := range []string{
		`{app="foo"} | logfmt bar,`,
		`{app="foo"} | logfmt ,bar`,
		`{app="foo"} | logfmt bar=`,
		`{app="foo"} | logfmt "bar"`,
	} {
		t.Run(input, func(t *testing.T) {
			p := &tool.LogQL{}
			_, err := p.Transform(input, &matchers)
			assert.Error(t, err)
		})
	}
}
