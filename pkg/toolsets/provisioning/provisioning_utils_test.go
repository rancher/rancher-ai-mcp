package provisioning

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
