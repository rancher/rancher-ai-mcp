package projects

import (
	"context"
	"errors"
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type commonTestClient struct {
	getResource  func(client.GetParams) (*unstructured.Unstructured, error)
	getResources func(client.ListParams) ([]*unstructured.Unstructured, error)
}

func (c *commonTestClient) GetClusterID(context.Context, string, string) (string, error) {
	return "", nil
}

func (c *commonTestClient) GetResource(_ context.Context, params client.GetParams) (*unstructured.Unstructured, error) {
	return c.getResource(params)
}

func (c *commonTestClient) GetResources(_ context.Context, params client.ListParams) ([]*unstructured.Unstructured, error) {
	return c.getResources(params)
}

func (c *commonTestClient) GetResourceInterface(context.Context, string, string, string, schema.GroupVersionResource) (dynamic.ResourceInterface, error) {
	return nil, nil
}

func project(name, displayName, backingNamespace string) *unstructured.Unstructured {
	object := map[string]any{
		"metadata": map[string]any{"name": name},
	}
	if displayName != "" {
		object["spec"] = map[string]any{"displayName": displayName}
	}
	if backingNamespace != "" {
		object["status"] = map[string]any{"backingNamespace": backingNamespace}
	}
	return &unstructured.Unstructured{Object: object}
}

func TestGetProjectID(t *testing.T) {
	directProject := project("p-direct", "Direct Project", "")
	matchedProject := project("p-matched", "Production", "")
	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "management.cattle.io", Resource: "projects"}, "requested")

	tests := map[string]struct {
		projectName string
		getResult   *unstructured.Unstructured
		getErr      error
		listResult  []*unstructured.Unstructured
		listErr     error
		wantID      string
		wantErr     string
		wantList    bool
	}{
		"returns project found by ID": {
			projectName: "p-direct",
			getResult:   directProject,
			wantID:      "p-direct",
		},
		"finds project by case-insensitive display name": {
			projectName: "production",
			getErr:      notFound,
			listResult:  []*unstructured.Unstructured{project("p-other", "Other", ""), matchedProject},
			wantID:      "p-matched",
			wantList:    true,
		},
		"returns not found after display name search": {
			projectName: "missing",
			getErr:      notFound,
			listResult:  []*unstructured.Unstructured{matchedProject},
			wantErr:     "project 'missing' not found in cluster 'c-123'",
			wantList:    true,
		},
		"returns direct lookup error": {
			projectName: "p-error",
			getErr:      errors.New("access denied"),
			wantErr:     "access denied",
		},
		"returns display name lookup error": {
			projectName: "production",
			getErr:      notFound,
			listErr:     errors.New("list denied"),
			wantErr:     "list denied",
			wantList:    true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			listCalled := false
			client := &commonTestClient{
				getResource: func(params client.GetParams) (*unstructured.Unstructured, error) {
					assert.Equal(t, client.GetParams{Cluster: LocalCluster, Kind: "project", Namespace: "c-123", Name: tt.projectName, Token: "token"}, params)
					return tt.getResult, tt.getErr
				},
				getResources: func(params client.ListParams) ([]*unstructured.Unstructured, error) {
					listCalled = true
					assert.Equal(t, client.ListParams{Cluster: LocalCluster, Kind: "project", Namespace: "c-123", Token: "token"}, params)
					return tt.listResult, tt.listErr
				},
			}

			projectID, projectResource, err := GetProjectID(t.Context(), client, "token", "c-123", tt.projectName)

			assert.Equal(t, tt.wantList, listCalled)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Empty(t, projectID)
				assert.Nil(t, projectResource)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, projectID)
			if tt.wantList {
				assert.Same(t, matchedProject, projectResource)
			} else {
				assert.Same(t, directProject, projectResource)
			}
		})
	}
}

func TestGetProjectBackingNamespace(t *testing.T) {
	tests := map[string]struct {
		project *unstructured.Unstructured
		want    string
		wantErr string
	}{
		"uses status backing namespace": {
			project: project("p-abc", "", "local-p-abc"),
			want:    "local-p-abc",
		},
		"falls back to project name": {
			project: project("p-abc", "", ""),
			want:    "p-abc",
		},
		"returns error for invalid backing namespace": {
			project: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"name": "p-abc"},
				"status":   map[string]any{"backingNamespace": true},
			}},
			wantErr: ".status.backingNamespace accessor error",
		},
		"returns error when no backing namespace or name exists": {
			project: &unstructured.Unstructured{Object: map[string]any{}},
			wantErr: "failed to get backing namespace for project",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			backingNamespace, err := GetProjectBackingNamespace(tt.project)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, backingNamespace)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, backingNamespace)
		})
	}
}
