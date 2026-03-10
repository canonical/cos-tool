package tool_test

import (
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func TestGetLabelMatchers(t *testing.T) {
	t.Run("single matcher", func(t *testing.T) {
		result, err := tool.GetLabelMatchers([]string{"env=prod"})
		assert.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod"}, result)
	})

	t.Run("multiple matchers", func(t *testing.T) {
		result, err := tool.GetLabelMatchers([]string{"env=prod", "region=us-east"})
		assert.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "prod", "region": "us-east"}, result)
	})

	t.Run("empty input", func(t *testing.T) {
		result, err := tool.GetLabelMatchers([]string{})
		assert.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("malformed matcher without equals sign", func(t *testing.T) {
		_, err := tool.GetLabelMatchers([]string{"invalid"})
		assert.Error(t, err)
	})
}

func TestLogQLValidateConfigAlwaysErrors(t *testing.T) {
	p := &tool.LogQL{}
	err := p.ValidateConfig("any_file.yaml")
	assert.Error(t, err)
}
