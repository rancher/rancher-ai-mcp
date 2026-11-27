package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newPhasePod(name, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"metadata": map[string]interface{}{"name": name},
		"status":   map[string]interface{}{"phase": phase},
	}}
}

func TestFilterByJSONPath(t *testing.T) {
	pods := []*unstructured.Unstructured{
		newPhasePod("a", "Running"),
		newPhasePod("b", "Pending"),
		newPhasePod("c", "Running"),
		{Object: map[string]interface{}{"metadata": map[string]interface{}{"name": "no-status"}}},
	}

	tests := map[string]struct {
		objs          []*unstructured.Unstructured
		expr          string
		expectedNames []string
		expectedError string
	}{
		"matches a subset": {
			objs:          pods,
			expr:          `@.status.phase=="Running"`,
			expectedNames: []string{"a", "c"},
		},
		"matches a single object": {
			objs:          pods,
			expr:          `@.status.phase=="Pending"`,
			expectedNames: []string{"b"},
		},
		"matches nothing": {
			objs:          pods,
			expr:          `@.status.phase=="Failed"`,
			expectedNames: []string{},
		},
		"objects missing the field are not matched": {
			objs:          pods,
			expr:          `@.status.phase!="Running"`,
			expectedNames: []string{"b"},
		},
		"no objects": {
			objs:          nil,
			expr:          `@.status.phase=="Running"`,
			expectedNames: []string{},
		},
		"invalid expression": {
			objs:          pods,
			expr:          `@.status.phase==`,
			expectedError: "invalid jsonPath filter",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			filtered, err := FilterByJSONPath(tt.objs, tt.expr)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, filtered)
			names := make([]string, 0, len(filtered))
			for _, obj := range filtered {
				names = append(names, obj.GetName())
			}
			assert.Equal(t, tt.expectedNames, names)
		})
	}
}
