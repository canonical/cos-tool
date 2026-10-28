package batch_test

import (
	"encoding/json"
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionIsOne(t *testing.T) {
	// The documented interface version. Changing it is a contract change.
	assert.Equal(t, 1, batch.Version)
}

func TestRequestUnmarshal(t *testing.T) {
	t.Run("full request", func(t *testing.T) {
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":"rule-42","expr":"up == 0",`+
			`"matchers":{"juju_model":"cos"},"format":"logql","version":1}`), &req)
		require.NoError(t, err)

		require.NotNil(t, req.ID)
		assert.Equal(t, "rule-42", *req.ID)
		require.NotNil(t, req.Expr)
		assert.Equal(t, "up == 0", *req.Expr)
		assert.Equal(t, batch.Matchers{"juju_model": "cos"}, req.Matchers)
		require.NotNil(t, req.Format)
		assert.Equal(t, "logql", *req.Format)
		require.NotNil(t, req.Version)
		assert.Equal(t, 1, *req.Version)
	})

	t.Run("minimal request leaves optional fields nil", func(t *testing.T) {
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":"rule-44","expr":"up == 0"}`), &req)
		require.NoError(t, err)

		assert.Nil(t, req.Matchers)
		assert.Nil(t, req.Format)
		assert.Nil(t, req.Version)
	})

	t.Run("absent is distinguishable from empty", func(t *testing.T) {
		var absent batch.Request
		require.NoError(t, json.Unmarshal([]byte(`{"expr":"up"}`), &absent))
		assert.Nil(t, absent.ID, "missing id must decode as nil")

		var empty batch.Request
		require.NoError(t, json.Unmarshal([]byte(`{"id":"","expr":"up"}`), &empty))
		require.NotNil(t, empty.ID, `an explicit "" id must not look absent`)
		assert.Equal(t, "", *empty.ID)
	})

	t.Run("unknown fields are ignored for forward compatibility", func(t *testing.T) {
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":"a","expr":"up","future_field":{"nested":true}}`), &req)
		require.NoError(t, err)
		assert.Equal(t, "a", *req.ID)
	})

	t.Run("wrong field type is an error", func(t *testing.T) {
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":42,"expr":"up"}`), &req)
		assert.Error(t, err)
	})

	t.Run("null matcher value is rejected", func(t *testing.T) {
		// null stays reserved as a future "unset this default" encoding
		// rather than being coerced to "".
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":"a","expr":"up","matchers":{"juju_model":null}}`), &req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "null")
	})

	t.Run("empty matcher value is allowed", func(t *testing.T) {
		var req batch.Request
		err := json.Unmarshal([]byte(`{"id":"a","expr":"up","matchers":{"juju_model":""}}`), &req)
		require.NoError(t, err)
		assert.Equal(t, batch.Matchers{"juju_model": ""}, req.Matchers)
	})
}

func TestResultMarshal(t *testing.T) {
	id := "rule-42"

	t.Run("ok result matches the documented key order", func(t *testing.T) {
		data, err := json.Marshal(batch.Result{
			Version: batch.Version,
			ID:      &id,
			Status:  batch.StatusOK,
			Expr:    `up{juju_model="cos"} == 0`,
		})
		require.NoError(t, err)
		assert.JSONEq(t, `{"version":1,"id":"rule-42","status":"ok",`+
			`"expr":"up{juju_model=\"cos\"} == 0"}`, string(data))
		assert.Equal(t,
			`{"version":1,"id":"rule-42","status":"ok","expr":"up{juju_model=\"cos\"} == 0"}`,
			string(data), "key order is part of the documented format")
	})

	t.Run("ok result omits line and error", func(t *testing.T) {
		data, err := json.Marshal(batch.Result{
			Version: batch.Version, ID: &id, Status: batch.StatusOK, Expr: "up",
		})
		require.NoError(t, err)
		assert.NotContains(t, string(data), "line")
		assert.NotContains(t, string(data), "error")
	})

	t.Run("malformed result without a recoverable id omits id", func(t *testing.T) {
		data, err := json.Marshal(batch.Result{
			Version: batch.Version,
			Line:    44,
			Status:  batch.StatusMalformed,
			Error:   `missing required field "id"`,
		})
		require.NoError(t, err)
		assert.Equal(t,
			`{"version":1,"line":44,"status":"malformed","error":"missing required field \"id\""}`,
			string(data))
	})

	t.Run("empty id is preserved rather than omitted", func(t *testing.T) {
		empty := ""
		data, err := json.Marshal(batch.Result{
			Version: batch.Version, ID: &empty, Status: batch.StatusOK, Expr: "up",
		})
		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":""`)
	})
}
