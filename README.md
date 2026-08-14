## MCP Server for Rancher

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

Each tool is exposed through the MCP protocol and can be invoked by the Rancher AI agent. The full, up-to-date list of tools grouped by toolset is maintained in [TOOLS.md](TOOLS.md), which is generated from the tool definitions.

To regenerate it after adding or changing tools, run:

```bash
go generate ./...
# or
make generate
```

## Configuration

### Command-line Flags

```bash
--port <int>              Port to listen on (default: 9092)
--insecure                Skip TLS verification (default: false)
```
