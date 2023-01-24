package tool_test

import (
	"fmt"
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	test_cases := []struct {
		filename string
		err bool
	}{
		{
			filename: "good_config.yml",
			err: false,
		},
		{
			filename: "bad_yaml.yml",
			err: true,
		},
		{
			filename: "bad_key.yml",
			err: true,
		},
	}

	checker := &tool.PromQL{}

	for _, test_case := range test_cases {
		err := checker.ValidateConfig(fmt.Sprintf("testdata/prom_configs/%s", test_case.filename))
		if test_case.err == true {
			assert.NotNil(t, err, "ValidateConfig returned unexpected result")
		} else {
			assert.Nil(t, err, "ValidateConfig returned unexpected result")
		}
	}
}
