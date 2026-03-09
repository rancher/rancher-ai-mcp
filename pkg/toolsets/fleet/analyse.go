package fleet

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"go.uber.org/zap"
)

type analyseFleetResourcesParams struct {
	Workspace string `json:"workspace"`
}

func (t *Tools) analyseFleetResources(ctx context.Context, toolReq *mcp.CallToolRequest, params analyseFleetResourcesParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("analyseFleetResources called")

	c := client.Client{}
	restCfg, err := c.CreateRestConfig(middleware.Token(ctx), t.rancherURL(toolReq), "local")
	if err != nil {
		zap.L().Error("failed to create rest config", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create rest config: %w", err)
	}

	report, err := t.resourceAnalyser.analiseFleetResources(ctx, restCfg, params.Workspace)
	if err != nil {
		zap.L().Error("failed to analyse fleet resources", zap.Error(err))
		return nil, nil, fmt.Errorf("failed to analyse fleet resources: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: report}},
	}, nil, nil
}
