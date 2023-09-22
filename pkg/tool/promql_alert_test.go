package tool_test

import (
	"io/ioutil"
	"path/filepath"
	"testing"

	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func readFile(filepath string) []byte {
	d, _ := ioutil.ReadFile(filepath)
	return d
}
func TestParsePromAlertFileSuccess(t *testing.T) {
	p := &tool.PromQL{}
	fp := filepath.Join("testdata/prom_alerts", "basic.yaml")
	_, errs := p.ValidateRules(fp, readFile(fp))
	assert.Nil(t, errs, "unexpected errors parsing file")
}

func TestParsePromAlertFileFailure(t *testing.T) {
	table := []struct {
		filename string
		errMsg   string
	}{
		{
			filename: "duplicate_group.yaml",
			errMsg:   "groupname: \"yolo\" is repeated in the same file",
		},
		{
			filename: "bad_expr.yaml",
			errMsg:   "could not parse expression",
		},
	}

	p := &tool.PromQL{}

	for _, c := range table {
		fp := filepath.Join("testdata/prom_alerts", c.filename)
		_, errs := p.ValidateRules(fp, readFile(fp))
		assert.NotNil(t, errs, "Expected error parsing %s but got none", c.filename)
		assert.Contains(t, errs.Error(), c.errMsg, "Expected error for %s.", c.filename)
	}
}
