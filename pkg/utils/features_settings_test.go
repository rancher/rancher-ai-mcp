package utils

import (
	"context"
	"errors"
	"testing"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// fakeResourceGetter serves the resources it is given, keyed by kind and name.
type fakeResourceGetter struct {
	resources map[string]*unstructured.Unstructured
	err       error
	calls     []client.GetParams
}

func (f *fakeResourceGetter) GetResource(_ context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	f.calls = append(f.calls, params)
	if f.err != nil {
		return nil, f.err
	}

	resource, ok := f.resources[params.Kind+"/"+params.Name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "management.cattle.io", Resource: params.Kind + "s"}, params.Name)
	}

	return resource, nil
}

func newTestFeature(name string, fields map[string]interface{}) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Feature",
		"metadata":   map[string]interface{}{"name": name},
	}
	for key, value := range fields {
		obj[key] = value
	}

	return &unstructured.Unstructured{Object: obj}
}

func newTestSetting(name string, fields map[string]interface{}) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "management.cattle.io/v3",
		"kind":       "Setting",
		"metadata":   map[string]interface{}{"name": name},
	}
	for key, value := range fields {
		obj[key] = value
	}

	return &unstructured.Unstructured{Object: obj}
}

func TestIsFeatureEnabled(t *testing.T) {
	const name = "test-feature"

	tests := map[string]struct {
		feature       map[string]interface{}
		missing       bool
		getErr        error
		expected      bool
		expectedError string
	}{
		"feature which does not exist is disabled": {
			missing:  true,
			expected: false,
		},
		"feature without any value is disabled": {
			feature:  map[string]interface{}{},
			expected: false,
		},
		"default enabled": {
			feature:  map[string]interface{}{"status": map[string]interface{}{"default": true}},
			expected: true,
		},
		"default disabled": {
			feature:  map[string]interface{}{"status": map[string]interface{}{"default": false}},
			expected: false,
		},
		"value overrides a default which is enabled": {
			feature: map[string]interface{}{
				"spec":   map[string]interface{}{"value": false},
				"status": map[string]interface{}{"default": true},
			},
			expected: false,
		},
		"value overrides a default which is disabled": {
			feature: map[string]interface{}{
				"spec":   map[string]interface{}{"value": true},
				"status": map[string]interface{}{"default": false},
			},
			expected: true,
		},
		"null value falls back to the default": {
			feature: map[string]interface{}{
				"spec":   map[string]interface{}{"value": nil},
				"status": map[string]interface{}{"default": true},
			},
			expected: true,
		},
		"locked value overrides the value": {
			feature: map[string]interface{}{
				"spec":   map[string]interface{}{"value": true},
				"status": map[string]interface{}{"lockedValue": false, "default": true},
			},
			expected: false,
		},
		"locked value enabled overrides the value": {
			feature: map[string]interface{}{
				"spec":   map[string]interface{}{"value": false},
				"status": map[string]interface{}{"lockedValue": true},
			},
			expected: true,
		},
		"value which is not a boolean": {
			feature:       map[string]interface{}{"spec": map[string]interface{}{"value": "true"}},
			expectedError: "failed to read spec.value of feature test-feature",
		},
		"locked value which is not a boolean": {
			feature:       map[string]interface{}{"status": map[string]interface{}{"lockedValue": "false"}},
			expectedError: "failed to read status.lockedValue of feature test-feature",
		},
		"default which is not a boolean": {
			feature:       map[string]interface{}{"status": map[string]interface{}{"default": int64(1)}},
			expectedError: "failed to read status.default of feature test-feature",
		},
		"spec which is not an object": {
			feature:       map[string]interface{}{"spec": "enabled"},
			expectedError: "failed to read spec.value of feature test-feature",
		},
		"error getting the feature": {
			getErr:        errors.New("connection refused"),
			expectedError: "failed to get feature test-feature: connection refused",
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			getter := &fakeResourceGetter{resources: map[string]*unstructured.Unstructured{}, err: tt.getErr}
			if !tt.missing && tt.feature != nil {
				getter.resources["feature/"+name] = newTestFeature(name, tt.feature)
			}

			ctx := middleware.WithToken(t.Context(), "token")
			enabled, err := IsFeatureEnabled(ctx, getter, name)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.False(t, enabled)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, enabled)

			// features are global, so they are read from the local cluster with the caller's token
			require.Len(t, getter.calls, 1)
			assert.Equal(t, client.GetParams{Cluster: "local", Kind: "feature", Name: name, Token: "token"}, getter.calls[0])
		})
	}
}

func TestGetSettingValue(t *testing.T) {
	const name = "test-setting"

	tests := map[string]struct {
		setting       map[string]interface{}
		missing       bool
		getErr        error
		expectedValue string
		expectedFound bool
		expectedError string
	}{
		"setting which does not exist": {
			missing:       true,
			expectedFound: false,
		},
		"value is used": {
			setting:       map[string]interface{}{"value": "true", "default": "false"},
			expectedValue: "true",
			expectedFound: true,
		},
		"empty value falls back to the default": {
			setting:       map[string]interface{}{"value": "", "default": "false"},
			expectedValue: "false",
			expectedFound: true,
		},
		"whitespace value falls back to the default": {
			setting:       map[string]interface{}{"value": "   ", "default": "true"},
			expectedValue: "true",
			expectedFound: true,
		},
		"missing value falls back to the default": {
			setting:       map[string]interface{}{"default": "true"},
			expectedValue: "true",
			expectedFound: true,
		},
		"setting with neither a value nor a default": {
			setting:       map[string]interface{}{},
			expectedValue: "",
			expectedFound: true,
		},
		"value which is not a string": {
			setting:       map[string]interface{}{"value": true},
			expectedError: "failed to read the value of setting test-setting",
		},
		"default which is not a string": {
			setting:       map[string]interface{}{"value": "", "default": true},
			expectedError: "failed to read the default of setting test-setting",
		},
		"error getting the setting": {
			getErr:        errors.New("connection refused"),
			expectedError: "failed to get setting test-setting: connection refused",
		},
	}

	for testName, tt := range tests {
		t.Run(testName, func(t *testing.T) {
			getter := &fakeResourceGetter{resources: map[string]*unstructured.Unstructured{}, err: tt.getErr}
			if !tt.missing && tt.setting != nil {
				getter.resources["setting/"+name] = newTestSetting(name, tt.setting)
			}

			ctx := middleware.WithToken(t.Context(), "token")
			value, found, err := GetSettingValue(ctx, getter, name)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.False(t, found)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedFound, found)
			assert.Equal(t, tt.expectedValue, value)

			require.Len(t, getter.calls, 1)
			assert.Equal(t, client.GetParams{Cluster: "local", Kind: "setting", Name: name, Token: "token"}, getter.calls[0])
		})
	}
}

func TestIsTrue(t *testing.T) {
	tests := map[string]struct {
		value    string
		expected bool
	}{
		"true":                  {value: "true", expected: true},
		"mixed case":            {value: "True", expected: true},
		"upper case":            {value: "TRUE", expected: true},
		"surrounding space":     {value: "  true\n", expected: true},
		"false":                 {value: "false", expected: false},
		"empty":                 {value: "", expected: false},
		"only whitespace":       {value: "  ", expected: false},
		"numeric truthy values": {value: "1", expected: false},
		"yes":                   {value: "yes", expected: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsTrue(tt.value))
		})
	}
}
