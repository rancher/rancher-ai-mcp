package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

// moveNamespacePlan plans moving a namespace to a project.
// It returns the JSON patch that would be applied to the namespace without actually applying it.
func (t *Tools) moveNamespacePlan(ctx context.Context, _ *mcp.CallToolRequest, params moveNamespaceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("moveNamespacePlan called", zap.String("namespace", params.Namespace), zap.String("project", params.Project), zap.String("cluster", params.Cluster))

	target, err := t.resolveMoveNamespace(ctx, params)
	if err != nil {
		zap.L().Error("failed to resolve namespace move", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, err
	}

	patch := append(
		metadataMapPatch("labels", target.namespace.GetLabels(), projectIDKey, target.label),
		metadataMapPatch("annotations", target.namespace.GetAnnotations(), projectIDKey, target.annotation)...,
	)

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		zap.L().Error("failed to marshal patch", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal patch: %w", err)
	}

	updateResource := response.PlanResource{
		Type:    response.OperationUpdate,
		Payload: json.RawMessage(patchBytes),
		Resource: response.Resource{
			Name:    target.namespace.GetName(),
			Kind:    "Namespace",
			Cluster: target.clusterID,
		},
	}

	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{updateResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "moveNamespacePlan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
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
