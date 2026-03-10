package core

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
)

const defaultListLimit = 10

// listKubernetesResourcesParams specifies the parameters needed to list kubernetes resources.
type listKubernetesResourcesParams struct {
	Namespace     string `json:"namespace" jsonschema:"the namespace of the resource"`
	Kind          string `json:"kind" jsonschema:"the kind of the resource"`
	Cluster       string `json:"cluster" jsonschema:"the cluster of the resource"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"maximum number of resources to return, defaults to 10"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector to filter resources (e.g. app=nginx)"`
}

// listKubernetesResources lists Kubernetes resources of a specific kind and namespace.
func (t *Tools) listKubernetesResources(ctx context.Context, toolReq *mcp.CallToolRequest, params listKubernetesResourcesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listKubernetesResource called")

	limit := params.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       params.Cluster,
		Kind:          params.Kind,
		Namespace:     params.Namespace,
		URL:           t.rancherURL(toolReq),
		Token:         middleware.Token(ctx),
		LabelSelector: params.LabelSelector,
	})
	if err != nil {
		zap.L().Error("failed to list resources", zap.String("tool", "listKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	total := int64(len(resources))
	truncated := total > limit
	if truncated {
		resources = resources[:limit]
	}

	var mcpResponse string
	if truncated {
		note := fmt.Sprintf("Results were limited to %d items out of %d total. There may be more resources matching the query. "+
			"Use a namespace or label selector to narrow results, or increase the limit.", limit, total)
		mcpResponse, err = response.CreateMcpResponse(resources, params.Cluster, note)
	} else {
		mcpResponse, err = response.CreateMcpResponse(resources, params.Cluster)
	}
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "listKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
