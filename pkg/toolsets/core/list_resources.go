package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"
)

const defaultListLimit = 10

// listKubernetesResourcesParams specifies the parameters needed to list kubernetes resources.
type listKubernetesResourcesParams struct {
	Namespace     string `json:"namespace" jsonschema:"the namespace where the resources are located. It must be empty for all namespaces or cluster-wide resources"`
	Kind          string `json:"kind" jsonschema:"the type of Kubernetes resource (e.g., Pod, Deployment, Service)"`
	Cluster       string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"maximum number of resources to return, defaults to 10. Do not change this value unless the user explicitly asks for a different page size or wants to see all the remaining results"`
	Offset        int64  `json:"offset,omitempty" jsonschema:"how many resources to skip from the start of the full list before returning results. Defaults to 0 (start at the first resource). Use it together with limit to page through results: set offset=0 for the first page, then increase offset by limit for each next page. For example, with limit=10: offset=0 returns resources 1-10, offset=10 returns resources 11-20, offset=20 returns resources 21-30. When more resources are available, the response tells you the exact offset to use for the next page"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"optional label selector to filter resources (e.g. app=nginx)"`
	JSONPath      string `json:"jsonPath,omitempty" jsonschema:"optional JSONPath filter predicate to select matching resources. Use @ to reference a resource, e.g. @.status.phase==\"Running\" or @.metadata.labels.app==\"nginx\". Only resources matching the predicate are returned"`
}

// listKubernetesResources lists Kubernetes resources of a specific kind and namespace.
func (t *Tools) listKubernetesResources(ctx context.Context, toolReq *mcp.CallToolRequest, params listKubernetesResourcesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("listKubernetesResource called")

	limit := params.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

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
		resources, err = filterByJSONPath(resources, params.JSONPath)
		if err != nil {
			zap.L().Error("failed to filter resources by jsonpath", zap.String("tool", "listKubernetesResource"), zap.Error(err))
			return nil, nil, err
		}
	}

	// The order of resources returned by the API is not guaranteed to be stable
	// across calls, so sort by namespace/name to make offset pagination deterministic.
	sort.Slice(resources, func(i, j int) bool {
		if ns := resources[i].GetNamespace(); ns != resources[j].GetNamespace() {
			return ns < resources[j].GetNamespace()
		}
		return resources[i].GetName() < resources[j].GetName()
	})

	total := int64(len(resources))

	// window the results in-memory: [offset, offset+limit)
	start := min(offset, total)
	end := min(offset+limit, total)
	resources = resources[start:end]

	hasMore := offset+limit < total

	var mcpResponse string
	if hasMore || offset > 0 {
		filterSuffix := ""
		if params.JSONPath != "" {
			filterSuffix = " matching the JSONPath filter"
		}
		note := fmt.Sprintf("Returned %d resources (offset %d, limit %d) out of %d total%s. "+
			"Use a namespace or label selector to narrow results, or increase the limit.",
			len(resources), offset, limit, total, filterSuffix)
		if hasMore {
			note += fmt.Sprintf(" To get the next page, set offset=%d.", offset+limit)
		}
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

// filterByJSONPath returns the subset of objs matching the given JSONPath predicate
// expression (the body of a kubectl-style [?(...)] filter, e.g. `@.status.phase=="Running"`).
// The objects are wrapped as a list so the filter can iterate over them, mirroring
// kubectl's `{.items[?(<predicate>)]}` selector semantics.
func filterByJSONPath(objs []*unstructured.Unstructured, expr string) ([]*unstructured.Unstructured, error) {
	items := make([]interface{}, len(objs))
	for i, obj := range objs {
		items[i] = obj.Object
	}
	input := map[string]interface{}{"items": items}

	jp := jsonpath.New("filter").AllowMissingKeys(true)
	if err := jp.Parse("{.items[?(" + expr + ")]}"); err != nil {
		return nil, fmt.Errorf("invalid jsonPath filter %q: %w", expr, err)
	}

	results, err := jp.FindResults(input)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate jsonPath filter %q: %w", expr, err)
	}

	filtered := make([]*unstructured.Unstructured, 0)
	for _, group := range results {
		for _, v := range group {
			if m, ok := v.Interface().(map[string]interface{}); ok {
				filtered = append(filtered, &unstructured.Unstructured{Object: m})
			}
		}
	}

	return filtered, nil
}
