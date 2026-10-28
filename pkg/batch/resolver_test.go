package batch_test

import (
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/canonical/cos-tool/pkg/tool"
	"github.com/stretchr/testify/assert"
)

func TestMapResolver(t *testing.T) {
	resolver := batch.NewMapResolver(map[string]batch.Transformer{
		"promql": &tool.PromQL{},
		"logql":  &tool.LogQL{},
	})

	t.Run("resolves known formats", func(t *testing.T) {
		for _, format := range []string{"promql", "logql"} {
			transformer, ok := resolver.Transformer(format)
			assert.True(t, ok, "%s must resolve", format)
			assert.NotNil(t, transformer)
		}
	})

	t.Run("is case-insensitive", func(t *testing.T) {
		for _, format := range []string{"PromQL", "LOGQL", "LogQl"} {
			_, ok := resolver.Transformer(format)
			assert.True(t, ok, "%s must resolve", format)
		}
	})

	t.Run("rejects unknown formats", func(t *testing.T) {
		for _, format := range []string{"sql", "", "prom"} {
			_, ok := resolver.Transformer(format)
			assert.False(t, ok, "%q must not resolve", format)
		}
	})

	t.Run("lists its formats for diagnostics", func(t *testing.T) {
		assert.ElementsMatch(t, []string{"promql", "logql"}, resolver.Formats())
	})
}

// The batch port must be satisfied by the existing implementations with no
// changes to pkg/tool.
var (
	_ batch.Transformer = (*tool.PromQL)(nil)
	_ batch.Transformer = (*tool.LogQL)(nil)
	_ batch.Transformer = (tool.Checker)(nil)
	_ tool.Transformer  = (tool.Checker)(nil)
)
