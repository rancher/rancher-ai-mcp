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
//
// The rancherServerURL can be empty in which case we'll fall back to the R_url
// value.
func AddAllTools(client *client.Client, mcpServer *mcp.Server, rancherServerURL string) {
	for _, ta := range allToolSets(client, rancherServerURL) {
		ta.AddTools(mcpServer)
	}
}

func allToolSets(client *client.Client, rancherURL string) []toolsAdder {
	return []toolsAdder{
		core.NewTools(client, rancherURL),
		fleet.NewTools(client, rancherURL),
		provisioning.NewTools(client, rancherURL),
	}
}
