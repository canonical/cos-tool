package tool_test

import (
	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestParseLokiAlertFileSuccess(t *testing.T) {
	p := &tool.LogQL{}
	_, errs := p.Validate(readFile(filepath.Join("testdata/loki_alerts", "basic.yaml")))
	assert.Nil(t, errs, "unexpected errors parsing file")
}

func TestParseLokiAlertFileFailure(t *testing.T) {
	table := []struct {
		filename string
		errMsg   string
	}{
		{
			filename: "duplicate_group.yaml",
			errMsg:   "groupname: \"testgroup\" is repeated in the same file",
		},
		{
			filename: "bad_expr.yaml",
			errMsg:   "syntax error",
		},
	}

	p := &tool.LogQL{}

	for _, c := range table {
		_, errs := p.Validate(readFile(filepath.Join("testdata/loki_alerts", c.filename)))
		assert.NotNil(t, errs, "Expected error parsing %s but got none", c.filename)
		assert.Contains(t, errs.Error(), c.errMsg, "Expected error for %s.", c.filename)
	}
}
