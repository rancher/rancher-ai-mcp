package core

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
)

// listKubernetesResourcesParams specifies the parameters needed to list kubernetes resources.
type listKubernetesResourcesParams struct {
	Namespace     string `json:"namespace" jsonschema:"the namespace where the resources are located. It must be empty for all namespaces or cluster-wide resources"`
	Kind          string `json:"kind" jsonschema:"the type of Kubernetes resource (e.g., Pod, Deployment, Service)"`
	Cluster       string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"maximum number of resources to return, defaults to 100"`
	Offset        int64  `json:"offset,omitempty" jsonschema:"how many resources to skip from the start of the full list before returning results. Defaults to 0 (start at the first resource). Use it together with limit to page through results: set offset=0 for the first page, then increase offset by limit for each next page. For example, with limit=10: offset=0 returns resources 1-10, offset=10 returns resources 11-20, offset=20 returns resources 21-30. When more resources are available, the response tells you the exact offset to use for the next page"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector to filter resources (e.g. app=nginx)"`
	JSONPath      string `json:"jsonPath,omitempty" jsonschema:"optional JSONPath filter predicate to select matching resources. Use @ to reference a resource, e.g. @.status.phase==\"Running\" or @.metadata.labels.app==\"nginx\". Only resources matching the predicate are returned"`
}

// listKubernetesResources lists Kubernetes resources of a specific kind and namespace.
func (t *Tools) listKubernetesResources(ctx context.Context, toolReq *mcp.CallToolRequest, params listKubernetesResourcesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listKubernetesResource called", zap.String("resourceKind", params.Kind))

	resources, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:       params.Cluster,
		Kind:          params.Kind,
		Namespace:     params.Namespace,
		Token:         middleware.Token(ctx),
		LabelSelector: params.LabelSelector,
	})
	if err != nil {
		zap.L().Error("failed to list resources", zap.String("tool", "listKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	if params.JSONPath != "" {
		resources, err = utils.FilterByJSONPath(resources, params.JSONPath)
		if err != nil {
			zap.L().Error("failed to filter resources by jsonpath", zap.String("tool", "listKubernetesResources"), zap.Error(err))
			return nil, nil, err
		}
	}

	page := t.paginator.SortAndPaginate(resources, params.Offset, params.Limit)

	filterSuffix := ""
	if params.JSONPath != "" {
		filterSuffix = " matching the JSONPath filter"
	}

	var mcpResponse string
	if note := t.paginator.BuildNote(page, filterSuffix); note != "" {
		mcpResponse, err = response.CreateMcpResponse(page.Items, params.Cluster, response.WithNotes(note))
	} else {
		mcpResponse, err = response.CreateMcpResponse(page.Items, params.Cluster)
	}
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "listKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}
