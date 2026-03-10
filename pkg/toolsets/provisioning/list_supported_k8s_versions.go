package provisioning

import (
	"context"
	"fmt"

	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type listSupportedK8sVersionsParams struct {
	Distribution string `json:"distribution" jsonschema:"the distribution of the cluster, either rke2 or k3s"`
}

func (t *Tools) listSupportedKubernetesVersions(_ context.Context, toolReq *mcp.CallToolRequest, params listSupportedK8sVersionsParams) (*mcp.CallToolResult, any, error) {
	if params.Distribution != "rke2" && params.Distribution != "k3s" {
		return nil, nil, fmt.Errorf("unsupported distribution: %s", params.Distribution)
	}

	log := utils.NewChildLogger(toolReq, map[string]string{
		"distribution": params.Distribution,
	})

	log.Debug("listSupportedKubernetesVersions called")

	versions, err := getKDMReleases(t.rancherURL(toolReq), params.Distribution)
	if err != nil {
		return nil, nil, err
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{{
		Object: map[string]interface{}{
			"message": fmt.Sprintf("Supported Kubernetes versions for %s: %v", params.Distribution, versions),
		},
	}}, "")
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: mcpResponse,
		}},
	}, nil, nil
}
