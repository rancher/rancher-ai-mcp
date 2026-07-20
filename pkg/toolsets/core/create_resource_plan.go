package core

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// createKubernetesResourcePlan plans the creation of a new Kubernetes resource.
// It returns the JSON representation of the resource to be created without actually creating it.
func (t *Tools) createKubernetesResourcePlan(_ context.Context, _ *mcp.CallToolRequest, params createKubernetesResourceParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createKubernetesResource_plan called")

	unstructuredObj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(params.Manifest), unstructuredObj); err != nil {
		zap.L().Error("failed to parse manifest", zap.String("tool", "createKubernetesResource_plan"), zap.Error(err))
		return nil, nil, fmt.Errorf("failed to parse manifest (expected YAML or JSON): %w", err)
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
