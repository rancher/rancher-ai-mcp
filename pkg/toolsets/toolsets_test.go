package toolsets

import (
	"testing"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllToolSets(t *testing.T) {
	c, err := client.NewClient(true, "")
	require.NoError(t, err)
	toolsets := allToolSets(c, false)

	assert.NotNil(t, toolsets)
	assert.Len(t, toolsets, 4, "should have exactly 4 toolsets (core, fleet, provisioning, and projects)")
}
