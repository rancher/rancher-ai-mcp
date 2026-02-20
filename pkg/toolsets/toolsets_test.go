package toolsets

import (
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
)

func TestAllToolSets(t *testing.T) {
	client := client.NewClient(true)
	toolsets := allToolSets(client, "https://notused.example.com")

	assert.NotNil(t, toolsets)
	assert.Len(t, toolsets, 3, "should have exactly 2 toolsets (core and fleet)")
}
