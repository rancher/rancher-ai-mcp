package utils

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForceArrayTypes(t *testing.T) {
	type rule struct {
		Verbs []string `json:"verbs"`
	}
	type params struct {
		Name  string   `json:"name"`
		Tags  []string `json:"tags,omitempty"`
		Rules []rule   `json:"rules"`
	}

	s, err := jsonschema.For[params](nil)
	require.NoError(t, err)
	require.Equal(t, []string{"null", "array"}, s.Properties["tags"].Types, "jsonschema-go is expected to infer nullable arrays")

	ForceArrayTypes(s)

	assert.Equal(t, "string", s.Properties["name"].Type)
	for _, prop := range []*jsonschema.Schema{s.Properties["tags"], s.Properties["rules"], s.Properties["rules"].Items.Properties["verbs"]} {
		assert.Equal(t, "array", prop.Type)
		assert.Nil(t, prop.Types)
	}
}
