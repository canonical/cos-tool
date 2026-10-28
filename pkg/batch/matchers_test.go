package batch_test

import (
	"testing"

	"github.com/canonical/cos-tool/pkg/batch"
	"github.com/stretchr/testify/assert"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		defaults map[string]string
		record   map[string]string
		want     map[string]string
	}{
		{
			name: "both empty",
			want: map[string]string{},
		},
		{
			name:     "defaults only",
			defaults: map[string]string{"juju_model": "cos"},
			want:     map[string]string{"juju_model": "cos"},
		},
		{
			name:   "record only",
			record: map[string]string{"juju_application": "proxy"},
			want:   map[string]string{"juju_application": "proxy"},
		},
		{
			name:     "disjoint keys union",
			defaults: map[string]string{"juju_model": "cos"},
			record:   map[string]string{"juju_application": "proxy"},
			want:     map[string]string{"juju_model": "cos", "juju_application": "proxy"},
		},
		{
			name:     "record wins on conflict",
			defaults: map[string]string{"juju_model": "cos"},
			record:   map[string]string{"juju_model": "other"},
			want:     map[string]string{"juju_model": "other"},
		},
		{
			name:     "record can override with an empty value but cannot remove",
			defaults: map[string]string{"juju_model": "cos"},
			record:   map[string]string{"juju_model": ""},
			want:     map[string]string{"juju_model": ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, batch.Merge(tc.defaults, tc.record))
		})
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	// The defaults map is shared across every record in a batch, so mutating
	// it would leak one record's matchers into all later records.
	defaults := map[string]string{"juju_model": "cos"}
	record := map[string]string{"juju_application": "proxy"}

	batch.Merge(defaults, record)

	assert.Equal(t, map[string]string{"juju_model": "cos"}, defaults)
	assert.Equal(t, map[string]string{"juju_application": "proxy"}, record)
}
