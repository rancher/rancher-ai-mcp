package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// createKubernetesResourcePlan plans the creation of a new Kubernetes resource.
// It returns the JSON representation of the resource to be created without actually creating it.
func (t *Tools) createKubernetesResourcePlan(_ context.Context, _ *mcp.CallToolRequest, params createKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Info("createKubernetesResource_plan called")

	objBytes, err := json.Marshal(params.Resource)
	if err != nil {
		zap.L().Error("failed to marshal resource", zap.String("tool", "createKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to marshal resource: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(objBytes, unstructuredObj); err != nil {
		zap.L().Error("failed to create unstructured resource", zap.String("tool", "createKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create unstructured object: %w", err)
	}

	createResource := response.NewCreateResourceInput(unstructuredObj, params.Cluster)
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zap.String("tool", "createKubernetesResource_plan"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
