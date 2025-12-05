package tool_test

import (
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
