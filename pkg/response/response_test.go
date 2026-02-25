package response

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCreateMCPResponse(t *testing.T) {
	tests := map[string]struct {
		objs           []*unstructured.Unstructured
		namespace      string
		cluster        string
		additionalInfo []string
		expected       string
		expectError    bool
	}{
		"single pod": {
			objs: []*unstructured.Unstructured{
				{
					Object: map[string]any{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]any{
							"name":      "test-pod",
							"namespace": "default",
						},
					},
				},
			},
			namespace:      "default",
			cluster:        "local",
			additionalInfo: []string{},
			expected:       `{"llm":[{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod","namespace":"default"}}],"uiContext":[{"namespace":"default","kind":"Pod","cluster":"local","name":"test-pod","type":"pod"}]}`,
			expectError:    false,
		},
		"single deployment": {
			objs: []*unstructured.Unstructured{
				{
					Object: map[string]any{
						"apiVersion": "v1",
						"kind":       "Deployment",
						"metadata": map[string]any{
							"name":      "test-deployment",
							"namespace": "default",
						},
					},
				},
			},
			namespace:      "default",
			cluster:        "local",
			additionalInfo: []string{},
			expected:       `{"llm":[{"apiVersion":"v1","kind":"Deployment","metadata":{"name":"test-deployment","namespace":"default"}}],"uiContext":[{"namespace":"default","kind":"Deployment","cluster":"local","name":"test-deployment","type":"apps.deployment"}]}`,
			expectError:    false,
		},
		"single pod with managedFields": {
			objs: []*unstructured.Unstructured{
				{
					Object: map[string]any{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]any{
							"name":      "test-pod",
							"namespace": "default",
							"managedFields": map[string]any{
								"apiVersion": "v1",
								"fieldsType": "FieldsV1",
							},
							"annotations": map[string]any{
								"kubectl.kubernetes.io/last-applied-configuration": "{}",
							},
						},
					},
				},
			},
			namespace:      "default",
			cluster:        "local",
			additionalInfo: []string{},
			expected:       `{"llm":[{"apiVersion":"v1","kind":"Pod","metadata":{"annotations":{},"name":"test-pod","namespace":"default"}}],"uiContext":[{"namespace":"default","kind":"Pod","cluster":"local","name":"test-pod","type":"pod"}]}`,
			expectError:    false,
		},
		"multiple pods": {
			objs: []*unstructured.Unstructured{
				{
					Object: map[string]any{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]any{
							"name":      "test-pod-1",
							"namespace": "default",
						},
					},
				},
				{
					Object: map[string]any{
						"apiVersion": "v1",
						"kind":       "Pod",
						"metadata": map[string]any{
							"name":      "test-pod-2",
							"namespace": "default",
						},
					},
				},
			},
			namespace:      "default",
			cluster:        "local",
			additionalInfo: []string{},
			expected:       `{"llm":[{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod-1","namespace":"default"}},{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test-pod-2","namespace":"default"}}],"uiContext":[{"namespace":"default","kind":"Pod","cluster":"local","name":"test-pod-1","type":"pod"},{"namespace":"default","kind":"Pod","cluster":"local","name":"test-pod-2","type":"pod"}]}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			resp, err := CreateMcpResponse(test.objs, test.cluster)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.JSONEq(t, test.expected, resp)
			}
		})
	}
}

func newUnstructured(name, namespace, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
}

func TestNewCreateResourceInput(t *testing.T) {
	tests := map[string]struct {
		obj     *unstructured.Unstructured
		cluster string
		want    PlanResource
	}{
		"basic pod": {
			obj:     newUnstructured("my-pod", "default", "Pod"),
			cluster: "local",
			want: PlanResource{
				Type: OperationCreate,
				Resource: Resource{
					Name:      "my-pod",
					Kind:      "Pod",
					Cluster:   "local",
					Namespace: "default",
				},
			},
		},
		"deployment in custom namespace": {
			obj:     newUnstructured("web-app", "production", "Deployment"),
			cluster: "downstream",
			want: PlanResource{
				Type: OperationCreate,
				Resource: Resource{
					Name:      "web-app",
					Kind:      "Deployment",
					Cluster:   "downstream",
					Namespace: "production",
				},
			},
		},
		"cluster-scoped resource (no namespace)": {
			obj:     newUnstructured("my-ns", "", "Namespace"),
			cluster: "local",
			want: PlanResource{
				Type: OperationCreate,
				Resource: Resource{
					Name:      "my-ns",
					Kind:      "Namespace",
					Cluster:   "local",
					Namespace: "",
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := NewCreateResourceInput(tc.obj, tc.cluster)
			assert.Equal(t, tc.want.Type, got.Type)
			assert.Equal(t, tc.want.Resource, got.Resource)
			assert.Equal(t, tc.obj, got.Payload)
		})
	}
}

func TestNewUpdateResourceInput(t *testing.T) {
	tests := map[string]struct {
		obj     *unstructured.Unstructured
		patch   []byte
		cluster string
		want    PlanResource
	}{
		"update pod - replace replicas": {
			obj:     newUnstructured("my-pod", "default", "Pod"),
			patch:   []byte(`[{"op":"replace","path":"/spec/replicas","value":3}]`),
			cluster: "local",
			want: PlanResource{
				Type: OperationUpdate,
				Resource: Resource{
					Name:      "my-pod",
					Kind:      "Pod",
					Cluster:   "local",
					Namespace: "default",
				},
			},
		},
		"update deployment - add label": {
			obj:     newUnstructured("api-server", "staging", "Deployment"),
			patch:   []byte(`[{"op":"add","path":"/metadata/labels/env","value":"staging"}]`),
			cluster: "staging-cluster",
			want: PlanResource{
				Type: OperationUpdate,
				Resource: Resource{
					Name:      "api-server",
					Kind:      "Deployment",
					Cluster:   "staging-cluster",
					Namespace: "staging",
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := NewUpdateResourceInput(tc.obj, tc.patch, tc.cluster)
			assert.Equal(t, tc.want.Type, got.Type)
			assert.Equal(t, tc.want.Resource, got.Resource)
			assert.Equal(t, tc.patch, got.Payload)
		})
	}
}

func TestCreatePlanResponse(t *testing.T) {
	tests := map[string]struct {
		resources   []PlanResource
		expected    string
		expectError bool
	}{
		"single create resource": {
			resources: []PlanResource{
				{
					Type: OperationCreate,
					Resource: Resource{
						Name:      "my-pod",
						Kind:      "Pod",
						Cluster:   "local",
						Namespace: "default",
					},
					Payload: map[string]any{
						"apiVersion": "v1",
						"kind":       "Pod",
					},
				},
			},
			expected: `[{"type":"create","payload":{"apiVersion":"v1","kind":"Pod"},"resource":{"name":"my-pod","kind":"Pod","cluster":"local","namespace":"default"}}]`,
		},
		"mixed operations": {
			resources: []PlanResource{
				{
					Type: OperationCreate,
					Resource: Resource{
						Name:      "new-deploy",
						Kind:      "Deployment",
						Cluster:   "local",
						Namespace: "default",
					},
					Payload: map[string]any{"kind": "Deployment"},
				},
				{
					Type: OperationUpdate,
					Resource: Resource{
						Name:      "existing-svc",
						Kind:      "Service",
						Cluster:   "local",
						Namespace: "default",
					},
					Payload: []any{map[string]any{"op": "replace", "path": "/spec/type", "value": "LoadBalancer"}},
				},
				{
					Type: OperationDelete,
					Resource: Resource{
						Name:      "old-pod",
						Kind:      "Pod",
						Cluster:   "local",
						Namespace: "kube-system",
					},
					Payload: nil,
				},
			},
			expected: `[{"type":"create","payload":{"kind":"Deployment"},"resource":{"name":"new-deploy","kind":"Deployment","cluster":"local","namespace":"default"}},{"type":"update","payload":[{"op":"replace","path":"/spec/type","value":"LoadBalancer"}],"resource":{"name":"existing-svc","kind":"Service","cluster":"local","namespace":"default"}},{"type":"delete","payload":null,"resource":{"name":"old-pod","kind":"Pod","cluster":"local","namespace":"kube-system"}}]`,
		},
		"empty resources": {
			resources: []PlanResource{},
			expected:  `[]`,
		},
		"nil resources": {
			resources: nil,
			expected:  `null`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := CreatePlanResponse(tc.resources)
			if tc.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, got)
		})
	}
}

func TestCreateMcpResponseAny(t *testing.T) {
	tests := map[string]struct {
		data        any
		uiContext   []UIContext
		expected    string
		expectError bool
	}{
		"map data without uiContext": {
			data:     map[string]any{"key": "value"},
			expected: `{"llm":{"key":"value"}}`,
		},
		"map data with uiContext entries": {
			data: map[string]any{"count": 2},
			uiContext: []UIContext{
				{Namespace: "default", Kind: "Pod", Cluster: "local", Name: "pod-1", Type: "pod"},
				{Namespace: "default", Kind: "Pod", Cluster: "local", Name: "pod-2", Type: "pod"},
			},
			expected: `{"llm":{"count":2},"uiContext":[{"namespace":"default","kind":"Pod","cluster":"local","name":"pod-1","type":"pod"},{"namespace":"default","kind":"Pod","cluster":"local","name":"pod-2","type":"pod"}]}`,
		},
		"string data": {
			data: "no resources found",
			uiContext: []UIContext{
				{Namespace: "default", Kind: "Pod", Cluster: "local", Name: "test-pod", Type: "pod"},
			},
			expected: `{"llm":"no resources found","uiContext":[{"namespace":"default","kind":"Pod","cluster":"local","name":"test-pod","type":"pod"}]}`,
		},
		"nil data": {
			data:     nil,
			expected: `{"llm":null}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			resp, err := CreateMcpResponseAny(test.data, test.uiContext...)
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.JSONEq(t, test.expected, resp)
			}
		})
	}
}
