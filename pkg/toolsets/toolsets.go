package toolsets

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets/core"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets/fleet"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets/provisioning"
)

// toolsAdder is an interface for types that can add tools to an MCP server.
type toolsAdder interface {
	AddTools(mcpServer *mcp.Server)
}

// AddAllTools adds all available tools to the MCP server.
func AddAllTools(client *client.Client, mcpServer *mcp.Server, readOnly bool) {
	for _, ta := range allToolSets(client, readOnly) {
		ta.AddTools(mcpServer)
	}
}

func allToolSets(client *client.Client, readOnly bool) []toolsAdder {
	return []toolsAdder{
		core.NewTools(client, readOnly),
		fleet.NewTools(client),
		provisioning.NewTools(client, readOnly),
	}
}
