package toolsets

import (
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllToolSets(t *testing.T) {
	c, err := client.NewClient(true, "https://fake-url")
	require.NoError(t, err)
	toolsets := allToolSets(c, false)

	assert.NotNil(t, toolsets)
	assert.Len(t, toolsets, 3, "should have exactly 2 toolsets (core and fleet)")
}
