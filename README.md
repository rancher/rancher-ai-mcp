## MCP Server for Rancher

> :warning: Warning! This project is in its very early stages of development. Expect frequent changes and potential breaking updates as we iterate on features and architecture.

The MCP server allows the [Rancher AI agent](https://github.com/rancher-sandbox/rancher-ai-agent) to securely retrieve or update Kubernetes and Rancher resources across local and downstream clusters. It expects the Rancher token in a header, which the agent will always provide for authentication.

## Overview

This Model Context Protocol (MCP) server provides a secure bridge between the Rancher AI agent and Kubernetes clusters, enabling AI-powered cluster management through a standardized tool interface. The server runs as a Kubernetes deployment within the Rancher environment and exposes tools for resource inspection, modification, and cluster operations.

## Architecture

### Package Structure

- **`cmd/`** - CLI commands and server initialization
  - `serve.go` - HTTP/TLS server setup with dynamic listener support
  - `root.go` - Root command configuration

- **`pkg/client/`** - Kubernetes client abstraction
  - Dynamic client wrapper with cluster ID resolution
  - Rancher API integration for cluster management
  - Support for both local and downstream cluster operations

- **`pkg/toolsets/`** - Tool registration and organization
  - `toolsets.go` - Central registry for tool collections
  - `core/` - Core Kubernetes operation tools

- **`pkg/response/`** - Response formatting utilities
  - Structured text and content generation for MCP responses

- **`pkg/converter/`** - Data transformation utilities
  - Group/Version/Resource (GVR) conversion helpers

### Multi-Agent Architecture

The server is designed with a modular toolset architecture to support a **multi-agent system**. Each toolset contains a collection of related tools that serve a specific agent or domain within the Rancher AI ecosystem.

**Current Toolsets:**
- **`core`** - Fundamental Kubernetes operations (resource management, pod inspection, metrics)

This architecture allows different AI agents to access only the tools they need, improving security, maintainability, and scalability. 

### TLS & Security

The server supports two modes:

1. **TLS Mode (Production)**: Uses Rancher's dynamic listener with auto-generated certificates
   - Certificates stored as Kubernetes secrets
   - Automatic cert rotation and renewal
   - Client certificate authentication support
   - TLS 1.2+ with secure cipher suites

2. **Insecure Mode (Development)**: Plain HTTP for local testing
   - Enabled via `--insecure` flag or `INSECURE_SKIP_TLS=true`

### Available Tools

Each tool is exposed through the MCP protocol and can be invoked by the Rancher AI agent:

| Tool                       | Description                                                                                  |
|----------------------------|----------------------------------------------------------------------------------------------|
| `getKubernetesResource`    | Retrieve a specific Kubernetes resource by name and type                                     |
| `patchKubernetesResource`  | Apply JSON patch operations to existing resources                                            |
| `listKubernetesResources`  | List all resources of a specific type in a namespace                                         |
| `inspectPod`               | Get detailed information about a pod including logs and events                               |
| `getDeployment`            | Retrieve deployment details with replica status                                              |
| `getNodeMetrics`           | Fetch resource usage metrics for cluster nodes                                               |
| `createKubernetesResource` | Create new Kubernetes resources from manifests                                               |
| `getClusterImages`         | List all container images used across the cluster                                            |
| `analyzeCluster`           | Retrieve multiple kubernetes resources related to a downstream cluster and its current state |
| `analyzeClusterMachines`   | Retrieve all Cluster API objects related to all machines within a downstream cluster         |
| `getClusterMachine`        | Retrieve all cluster API objects related to a specific machine within a downstream cluster   |
| `listKubernetesVersions`   | Lists all of the RKE2 and K3s versions that Rancher can provision.                           |
| `createImportedCluster`    | Creates an imported cluster using the provided name and settings                             |
| `createCustomCluster`      | Creates a custom cluster using the provided name and settings                                |
| `scaleClusterNodePool`     | Scales an existing node pool within a Rancher provisioned cluster up or down                 |

## Configuration

### Command-line Flags

```bash
--port <int>              Port to listen on (default: 9092)
--insecure                Skip TLS verification (default: false)
```