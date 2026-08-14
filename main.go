package main

//go:generate go run ./internal/toolsdoc

import (
	"github.com/rancher/rancher-ai-mcp/cmd"
)

func main() {
	cmd.Execute()
}
