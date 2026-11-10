package provisioning

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestValidateClusterName(t *testing.T) {
	const invalidNameError = "cluster names may only contain lowercase alphanumeric characters, hyphens, and must be less than 63 characters long"

	tests := []struct {
		name          string
		clusterName   string
		expectedError string
	}{
		{name: "local cluster", clusterName: "local"},
		{name: "fleet cluster", clusterName: "my-cluster-1"},
		{name: "management cluster", clusterName: "c-abc12"},
		{name: "minimum length", clusterName: "a1"},
		{name: "maximum length", clusterName: strings.Repeat("a", 63)},
		{name: "missing name", expectedError: "cluster name is required"},
		{name: "single letter", clusterName: "a", expectedError: invalidNameError},
		{name: "single digit", clusterName: "1", expectedError: invalidNameError},
		{name: "leading hyphen", clusterName: "-cluster", expectedError: invalidNameError},
		{name: "leading underscore", clusterName: "_cluster", expectedError: invalidNameError},
		{name: "leading slash", clusterName: "/cluster", expectedError: invalidNameError},
		{name: "uppercase first character", clusterName: "Cluster", expectedError: invalidNameError},
		{name: "uppercase middle character", clusterName: "myCluster", expectedError: invalidNameError},
		{name: "invalid character", clusterName: "my_cluster", expectedError: invalidNameError},
		{name: "too long", clusterName: strings.Repeat("a", 64), expectedError: "cluster name must be at most 63 characters"},
		{name: "ends in invalid character", clusterName: "mycluster-", expectedError: "cluster names may only contain lowercase alphanumeric characters, hyphens, and must be less than 63 characters long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClusterName(tt.clusterName)
			if tt.expectedError == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedError)
			}
		})
	}
}

func TestRemoveMachinePoolSchemas(t *testing.T) {
	tests := map[string]struct {
		obj      map[string]any
		expected map[string]any
	}{
		"machine pools with and without a schema": {
			obj: map[string]any{"spec": map[string]any{"rkeConfig": map[string]any{"machinePools": []any{
				map[string]any{"name": "cp", "quantity": int64(1), "dynamicSchemaSpec": `{"resourceFields":{}}`},
				map[string]any{"name": "worker", "quantity": int64(2)},
			}}}},
			expected: map[string]any{"spec": map[string]any{"rkeConfig": map[string]any{"machinePools": []any{
				map[string]any{"name": "cp", "quantity": int64(1)},
				map[string]any{"name": "worker", "quantity": int64(2)},
			}}}},
		},
		"cluster without an rke config": {
			obj:      map[string]any{"spec": map[string]any{"kubernetesVersion": "v1.36.5+k3s1"}},
			expected: map[string]any{"spec": map[string]any{"kubernetesVersion": "v1.36.5+k3s1"}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: tt.obj}
			removeMachinePoolSchemas(obj)
			assert.Equal(t, tt.expected, obj.Object)
		})
	}
}

func TestValidateInt32(t *testing.T) {
	tests := map[string]struct {
		value         int
		expectedError string
	}{
		"zero":            {value: 0},
		"maximum int32":   {value: math.MaxInt32},
		"minimum int32":   {value: math.MinInt32},
		"above the range": {value: math.MaxInt32 + 1, expectedError: "size (2147483648) is out of range, it must be between -2147483648 and 2147483647"},
		"below the range": {value: math.MinInt32 - 1, expectedError: "size (-2147483649) is out of range, it must be between -2147483648 and 2147483647"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateInt32("size", tt.value)
			if tt.expectedError != "" {
				assert.EqualError(t, err, tt.expectedError)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestRelatedTool(t *testing.T) {
	assert.Equal(t, "scaleClusterNodePool", relatedTool(newToolRequest("updateNodePoolAutoscaling"), "scaleClusterNodePool"))
	assert.Equal(t, "scaleClusterNodePoolPlan", relatedTool(newToolRequest("updateNodePoolAutoscalingPlan"), "scaleClusterNodePool"))
	assert.Equal(t, "scaleClusterNodePool", relatedTool(nil, "scaleClusterNodePool"))
}
