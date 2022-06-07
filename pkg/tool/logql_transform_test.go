package tool_test

import (
	"github.com/canonical/cos-tool/pkg/tool"
	"testing"

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
			Matchers: map[string]string{"model": "lma"},
			Expected: `{job="loki", model="lma"} !~ ".+"`,
		},
		{
			Input:    `{cool="breeze"} |= "weather"`,
			Matchers: map[string]string{"hot": "sunrays", "dance": "macarena"},
			Expected: `{cool="breeze", hot="sunrays", dance="macarena"} |= "weather"`,
		},
	}
	for _, c := range cases {
		p := &tool.LogQL{}
		out, err := p.Transform(c.Input, &c.Matchers)
		assert.NoError(t, err)
		assert.Equal(t, c.Expected, out)
	}
}
