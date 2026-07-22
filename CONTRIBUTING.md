# Contributing to Rancher MCP Server

Thank you for your interest in contributing to the Rancher AI MCP Server! This guide will help you get started with development and understand our contribution process.

## Getting Started

### Prerequisites

- Go 1.24 or later
- Access to a Kubernetes cluster (for testing)
- Basic understanding of Kubernetes and the Model Context Protocol (MCP)

### Setting Up Your Development Environment

1. **Fork and Clone the Repository**

```bash
git clone https://github.com/<your-username>/rancher-ai-mcp.git
cd rancher-ai-mcp
```

2. **Install Dependencies**

```bash
go mod download
```

3. **Run Tests**

```bash
go test -v -cover ./...
```

4. **Build the Project**

```bash
go build -o mcp-server .
```

### Running Locally for Development

When running the MCP server locally (outside of a Kubernetes cluster), set the `RANCHER_URL` environment variable to point to your Rancher instance. This skips the in-cluster auto-detection and lets the server connect directly to your Rancher API:

If `RANCHER_URL` is not set and the server is not running inside a cluster, startup will fail because it cannot auto-discover the Rancher URL.

### Project Structure

```
pkg/
├── client/         # Kubernetes client wrapper - use it to fetch/update/create resources in Kubernetes clusters
├── toolsets/       # Tool collections
│   ├── core/      # Core Kubernetes tools
│   ├── security/  # Security-related tools (example)
│   └── ...        # Other domain-specific toolsets
├── response/       # Response formatting utilities
└── converter/      # Data transformation utilities
```

### Adding a Tool to an Existing Toolset

To add a new tool to an existing toolset (e.g., adding a new tool to the `core` toolset):

1. **Create a new handler file** in the toolset directory (e.g., `pkg/toolsets/core/your_tool.go`):

```go
package core

import (
    "github.com/rancher/rancher-ai-mcp/pkg/response"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// handleYourNewTool handles the yourNewTool tool invocation
func (t *Tools) handleYourNewTool(ctx context.Context, toolReq *mcp.CallToolRequest, params YourParams) (*mcp.CallToolResult, any, error) {    
    // Use the client to interact with Kubernetes if needed
    result, err := t.client.GetResource(/* parameters */)
    if err != nil {
        return nil, nil, err
    }
    
    return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: stringResult}},
	}, nil, nil
}
```

2. **Register the tool** in the toolset's `tools.go` file by adding it to the `AddTools()` method:

```go
func (t *Tools) AddTools(mcpServer *mcp.Server) {
    // ... existing tools ...
    
    mcp.AddTool(mcpServer, &mcp.Tool{
        Name:        "yourNewTool",
        Description: "Clear description of what the tool does",
        Meta:    map[string]any{"toolset": "your-toolset"}, 
        Handler: t.handleYourNewTool,
    })
}
```

3. **Create a test file** (e.g., `pkg/toolsets/core/your_tool_test.go`) following the existing test patterns:

```go
package core

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestHandleYourNewTool(t *testing.T) {
    // Add unit tests here
}
```

4. **Run tests** to ensure everything works:

```bash
go test -v ./pkg/toolsets/core
```

5. **Update documentation** in README.md to list the new tool in the Available Tools table

### Adding a New Toolset

1. Create a new directory under `pkg/toolsets/` (e.g., `pkg/toolsets/security/`)
2. Implement the `toolsAdder` interface:

```go
type toolsAdder interface {
    AddTools(mcpServer *mcp.Server)
}
```

3. Create a `tools.go` file with your tool implementations:

```go
package security

import (
    "github.com/rancher/rancher-ai-mcp/pkg/client"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Tools struct {
    client *client.Client
}

func NewTools(client *client.Client) *Tools {
    return &Tools{client: client}
}

func (t *Tools) AddTools(mcpServer *mcp.Server) {
    mcp.AddTool(mcpServer, &mcp.Tool{
        Name: "scanForVulnerabilities",
        Description: "Scan cluster for security vulnerabilities",
        Meta: map[string]any{"toolset": "security"}, // make sure this is unique for this toolset
        Handler: t.handleVulnerabilityScan,
    })
    // Add more tools here...
}
```
4. Make sure the toolset annotation is unique
5. Add comprehensive tests in `tools_test.go`
6. Update `pkg/toolsets/toolsets.go` to include your new toolset
7. Update documentation in README.md

### Working with Response Formatting

The `pkg/response` package provides utilities for formatting tool responses. There are two main ways to return data:

#### Simple Text Response

For basic responses without UI integration, use `mcp.TextContent`:

```go
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "your response in string here"}},
	}, nil, nil
```

This returns plain text or formatted data that the AI agent can process.

#### Response with UI Context

When you want to enable the **Rancher UI to display clickable links** to Kubernetes resources, use `response.CreateMcpResponse()`:

```go
mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, params.Cluster)
	if err != nil {
		zap.L().Error("failed to create mcp response", zap.String("tool", "updateKubernetesResource"), zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
```

**Key Benefits of `CreateMcpResponse()`:**
- Automatically extracts resource metadata (namespace, kind, name, cluster)
- Generates UI context that the Rancher UI uses to create clickable resource links
- Removes `managedFields` to reduce payload size

**When to use each:**
- Use `mcp.TextContent` for: Simple text responses, status messages, errors, or non-resource data
- Use `CreateMcpResponse()` for: Any response containing Kubernetes resources that users might want to view in the UI

## Reporting Issues

When reporting bugs or requesting features:

1. **Search Existing Issues** - Check if it's already reported
2. **Use Issue Templates** - Follow the provided templates
3. **Provide Details**:
   - Clear description of the issue or feature
   - Steps to reproduce (for bugs)
   - Expected vs actual behavior
   - Environment details (Go version, Rancher version, Kubernetes version, etc.)
   - Relevant logs or error messages
