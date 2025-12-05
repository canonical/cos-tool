package log

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
)

func TestLabelsBuilderBasicOperations(t *testing.T) {
	base := NewBaseLabelsBuilder()
	lbs := base.ForLabels(labels.EmptyLabels(), 0)

	// Test initial state
	value, ok := lbs.Get("nonexistent")
	assert.False(t, ok)
	assert.Empty(t, value)

	// Test Set and Get
	lbs.Set("key1", "value1")
	value, ok = lbs.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", value)

	// Test BaseHas (for checking base labels)
	assert.False(t, lbs.BaseHas("key1"))

	// Test updating existing key
	lbs.Set("key1", "updated_value1")
	value, ok = lbs.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "updated_value1", value)

	// Test multiple keys
	lbs.Set("key2", "value2")
	lbs.Set("key3", "value3")

	value, ok = lbs.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, "value2", value)

	value, ok = lbs.Get("key3")
	assert.True(t, ok)
	assert.Equal(t, "value3", value)

	// Test Map()
	lbsMap := lbs.Map()
	assert.Equal(t, 3, len(lbsMap))
	assert.Equal(t, "updated_value1", lbsMap["key1"])
	assert.Equal(t, "value2", lbsMap["key2"])
	assert.Equal(t, "value3", lbsMap["key3"])

	// Test Del
	lbs.Del("key2")
	value, ok = lbs.Get("key2")
	assert.False(t, ok)
	assert.Empty(t, value)

	lbsMap = lbs.Map()
	assert.Equal(t, 2, len(lbsMap))
	assert.Equal(t, "updated_value1", lbsMap["key1"])
	assert.Equal(t, "value3", lbsMap["key3"])
}

func TestLabelsBuilderErrorHandling(t *testing.T) {
	base := NewBaseLabelsBuilder()
	lbs := base.ForLabels(labels.EmptyLabels(), 0)

	// Test initial state has no error
	assert.False(t, lbs.HasErr())
	assert.Equal(t, "", lbs.GetErr())

	// Test setting error
	lbs.SetErr("test error message")
	assert.True(t, lbs.HasErr())
	assert.Equal(t, "test error message", lbs.GetErr())

	// Test that error doesn't affect normal operations
	lbs.Set("key", "value")
	value, ok := lbs.Get("key")
	assert.True(t, ok)
	assert.Equal(t, "value", value)
	assert.True(t, lbs.HasErr()) // error should persist

	// Test clearing error
	lbs.SetErr("")
	assert.False(t, lbs.HasErr())
	assert.Equal(t, "", lbs.GetErr())
}

func TestLabelsBuilderWithInitialLabels(t *testing.T) {
	// Create initial labels
	initialLabels := labels.FromStrings(
		"existing1", "value1",
		"existing2", "value2",
	)

	base := NewBaseLabelsBuilder()
	lbs := base.ForLabels(initialLabels, initialLabels.Hash())

	// Should have initial labels
	assert.True(t, lbs.BaseHas("existing1"))
	assert.True(t, lbs.BaseHas("existing2"))

	value, ok := lbs.Get("existing1")
	assert.True(t, ok)
	assert.Equal(t, "value1", value)

	value, ok = lbs.Get("existing2")
	assert.True(t, ok)
	assert.Equal(t, "value2", value)

	// Should be able to add new labels
	lbs.Set("new1", "newvalue1")
	value, ok = lbs.Get("new1")
	assert.True(t, ok)
	assert.Equal(t, "newvalue1", value)

	// Should still have old labels
	assert.True(t, lbs.BaseHas("existing1"))
	value, ok = lbs.Get("existing1")
	assert.True(t, ok)
	assert.Equal(t, "value1", value)

	// Total count should be 3
	lbsMap := lbs.Map()
	assert.Equal(t, 3, len(lbsMap))
}

func TestLabelsBuilderReset(t *testing.T) {
	base := NewBaseLabelsBuilder()
	lbs := base.ForLabels(labels.EmptyLabels(), 0)

	// Add some labels and error
	lbs.Set("key1", "value1")
	lbs.Set("key2", "value2")
	lbs.SetErr("error message")

	value, ok := lbs.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", value)

	value, ok = lbs.Get("key2")
	assert.True(t, ok)
	assert.Equal(t, "value2", value)

	assert.True(t, lbs.HasErr())

	// Reset with new labels
	lbs.Reset()

	// Should be empty now
	value, ok = lbs.Get("key1")
	assert.False(t, ok)
	assert.Empty(t, value)

	value, ok = lbs.Get("key2")
	assert.False(t, ok)
	assert.Empty(t, value)

	value, ok = lbs.Get("reset1")
	assert.False(t, ok)
	assert.Empty(t, value)

	// Error should be cleared
	assert.False(t, lbs.HasErr())
	assert.Equal(t, "", lbs.GetErr())
}

func TestLabelsResult(t *testing.T) {
	// Create some test labels
	testLabels := labels.FromStrings(
		"test", "value",
		"env", "production",
	)

	// Create LabelsResult
	result := NewLabelsResult(testLabels, 12345)

	// Test interface methods
	assert.Equal(t, uint64(12345), result.Hash())
	assert.Equal(t, "{env=\"production\", test=\"value\"}", result.String())

	// Test that Labels() returns the original labels
	returnedLabels := result.Labels()
	assert.Equal(t, 2, returnedLabels.Len())
	assert.Equal(t, "value", returnedLabels.Get("test"))
	assert.Equal(t, "production", returnedLabels.Get("env"))
}

func TestLabelsBuilderSpecialCharacters(t *testing.T) {
	base := NewBaseLabelsBuilder()
	lbs := base.ForLabels(labels.EmptyLabels(), 0)

	// Test with special characters in keys and values
	testCases := map[string]string{
		"unicode-key":   "héllo-wörld",
		"quotes":        `value with "quotes"`,
		"spaces":        "value with spaces",
		"special-chars": "test@#$%^&*()",
		"empty":         "",
	}

	for key, value := range testCases {
		lbs.Set(key, value)
		retrievedValue, ok := lbs.Get(key)
		assert.True(t, ok)
		assert.Equal(t, value, retrievedValue)
	}

	// Verify all are present in Map()
	lbsMap := lbs.Map()
	assert.Equal(t, len(testCases), len(lbsMap))
	for key, value := range testCases {
		assert.Equal(t, value, lbsMap[key])
	}
}
