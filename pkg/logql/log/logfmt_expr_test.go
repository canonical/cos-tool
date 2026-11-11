package log

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func processLogfmtExpressions(t *testing.T, base labels.Labels, line string, expressions ...LogfmtExpression) *LabelsBuilder {
	t.Helper()

	parser, err := NewLogfmtExpressionParser(expressions)
	require.NoError(t, err)

	lbs := NewBaseLabelsBuilder().ForLabels(base, 0)
	out, ok := parser.Process([]byte(line), lbs)
	require.True(t, ok)
	assert.Equal(t, line, string(out))

	return lbs
}

func TestLogfmtExpressionParserExtractsOnlyListedKeys(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.EmptyLabels(),
		`level=info path=/status duration=15ms`,
		NewLogfmtExpr("path", "path"),
		NewLogfmtExpr("duration", "duration"),
	)

	path, ok := lbs.Get("path")
	assert.True(t, ok)
	assert.Equal(t, "/status", path)

	duration, ok := lbs.Get("duration")
	assert.True(t, ok)
	assert.Equal(t, "15ms", duration)

	_, ok = lbs.Get("level")
	assert.False(t, ok)
}

func TestLogfmtExpressionParserRenames(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.EmptyLabels(),
		`level=info path=/status`,
		NewLogfmtExpr("renamed", "path"),
	)

	renamed, ok := lbs.Get("renamed")
	assert.True(t, ok)
	assert.Equal(t, "/status", renamed)

	_, ok = lbs.Get("path")
	assert.False(t, ok)
}

func TestLogfmtExpressionParserSameKeyForSeveralLabels(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.EmptyLabels(),
		`path=/status`,
		NewLogfmtExpr("first", "path"),
		NewLogfmtExpr("second", "path"),
	)

	first, _ := lbs.Get("first")
	second, _ := lbs.Get("second")
	assert.Equal(t, "/status", first)
	assert.Equal(t, "/status", second)
}

func TestLogfmtExpressionParserKeepsBaseLabels(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.FromStrings("path", "base"),
		`path=/status`,
		NewLogfmtExpr("path", "path"),
	)

	extracted, ok := lbs.Get("path_extracted")
	assert.True(t, ok)
	assert.Equal(t, "/status", extracted)
}

func TestLogfmtExpressionParserMissingKey(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.EmptyLabels(),
		`level=info`,
		NewLogfmtExpr("path", "path"),
	)

	_, ok := lbs.Get("path")
	assert.False(t, ok)
	assert.Empty(t, lbs.GetErr())
}

func TestLogfmtExpressionParserMalformedLine(t *testing.T) {
	lbs := processLogfmtExpressions(t, labels.EmptyLabels(),
		`path="unterminated`,
		NewLogfmtExpr("path", "path"),
	)

	assert.Equal(t, errLogfmt, lbs.GetErr())
}

func TestNewLogfmtExpressionParserInvalidLabelName(t *testing.T) {
	_, err := NewLogfmtExpressionParser([]LogfmtExpression{NewLogfmtExpr("", "path")})
	assert.Error(t, err)
}
