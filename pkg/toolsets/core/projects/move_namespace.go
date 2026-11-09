package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const projectIDKey = "field.cattle.io/projectId"

type moveNamespaceParams struct {
	Namespace string `json:"namespace" jsonschema:"the name of the namespace to move"`
	Project   string `json:"project" jsonschema:"the name or ID of the destination project"`
	Cluster   string `json:"cluster" jsonschema:"the name of the cluster containing the namespace and project"`
}

// moveNamespaceTarget holds the resolved data needed to move a namespace to a project.
type moveNamespaceTarget struct {
	clusterID  string
	projectID  string
	namespace  *unstructured.Unstructured
	annotation string
}

// resolveMoveNamespace resolves the cluster and project IDs and fetches the namespace to be moved.
func (t *Tools) resolveMoveNamespace(ctx context.Context, params moveNamespaceParams) (*moveNamespaceTarget, error) {
	clusterID, err := t.client.GetClusterID(ctx, middleware.Token(ctx), params.Cluster)
	if err != nil {
		return nil, err
	}

	projectID, _, err := GetProjectID(ctx, t.client, middleware.Token(ctx), clusterID, params.Project)
	if err != nil {
		return nil, err
	}

	namespace, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: clusterID,
		Kind:    "namespace",
		Name:    params.Namespace,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace %q: %w", params.Namespace, err)
	}

	return &moveNamespaceTarget{
		clusterID:  clusterID,
		projectID:  projectID,
		namespace:  namespace,
		annotation: fmt.Sprintf("%s:%s", clusterID, projectID),
	}, nil
}

// moveNamespace assigns a namespace to a project.
func (t *Tools) moveNamespace(ctx context.Context, toolReq *mcp.CallToolRequest, params moveNamespaceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("moveNamespace called", zap.String("namespace", params.Namespace), zap.String("project", params.Project), zap.String("cluster", params.Cluster))

	target, err := t.resolveMoveNamespace(ctx, params)
	if err != nil {
		return nil, nil, err
	}

	patchBytes, err := json.Marshal(target.patch())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	resourceInterface, err := t.client.GetResourceInterface(
		ctx, middleware.Token(ctx), "", target.clusterID, converter.K8sKindsToGVRs["namespace"])
	if err != nil {
		return nil, nil, err
	}

	updatedNamespace, err := resourceInterface.Patch(ctx, target.namespace.GetName(), types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to move namespace %q: %w", params.Namespace, err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{updatedNamespace}, target.clusterID)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// patch builds the JSON patch operations that set the project ID label and annotation on the namespace.
func (t *moveNamespaceTarget) patch() []map[string]any {
	return append(
		metadataMapPatch("labels", t.namespace.GetLabels(), projectIDKey, t.projectID),
		metadataMapPatch("annotations", t.namespace.GetAnnotations(), projectIDKey, t.annotation)...,
	)
}

// metadataMapPatch builds JSON patch operations that set key=value in the given metadata map field
// (e.g. labels or annotations). If the map does not exist yet, it is created.
func metadataMapPatch(field string, current map[string]string, key, value string) []map[string]any {
	if current == nil {
		return []map[string]any{{
			"op":    "add",
			"path":  "/metadata/" + field,
			"value": map[string]string{key: value},
		}}
	}

	op := "add"
	if _, ok := current[key]; ok {
		op = "replace"
	}

	return []map[string]any{{
		"op":    op,
		"path":  fmt.Sprintf("/metadata/%s/%s", field, escapeJSONPointer(key)),
		"value": value,
	}}
}

// escapeJSONPointer escapes a JSON pointer reference token as defined in RFC 6901.
func escapeJSONPointer(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}
