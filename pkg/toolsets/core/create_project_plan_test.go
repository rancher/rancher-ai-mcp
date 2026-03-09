package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProjectPlan(t *testing.T) {
	fakeUrl := "https://localhost:8080"
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params         createProjectParams
		expectedError  string
		validateResult func(t *testing.T, result string)
	}{
		"create project plan": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "test-project",
				DisplayName: "Test Project",
				Description: "A test project",
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				assert.Equal(t, response.OperationCreate, planResource.Type)
				assert.Equal(t, "test-project", planResource.Resource.Name)
				assert.Equal(t, "Project", planResource.Resource.Kind)
				assert.Equal(t, "local", planResource.Resource.Cluster)
				assert.Equal(t, "local", planResource.Resource.Namespace)

				// Validate the payload contains the expected project structure
				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "management.cattle.io/v3", payload["apiVersion"])
				assert.Equal(t, "Project", payload["kind"])

				metadata, ok := payload["metadata"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "test-project", metadata["name"])
				assert.Equal(t, "local", metadata["namespace"])

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "local", spec["clusterName"])
				assert.Equal(t, "Test Project", spec["displayName"])
				assert.Equal(t, "A test project", spec["description"])
			},
		},
		"create project plan with resource limits": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "project-with-limits",
				DisplayName: "Project with Limits",
				ContainerDefaultResourceLimit: containerDefaultResourceLimit{
					CPULimit:          2000,
					CPUReservation:    1000,
					MemoryLimit:       4096,
					MemoryReservation: 2048,
				},
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)

				resourceLimits, ok := spec["containerDefaultResourceLimit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "2000m", resourceLimits["limitsCpu"])
				assert.Equal(t, "1000m", resourceLimits["requestsCpu"])
				assert.Equal(t, "4096Mi", resourceLimits["limitsMemory"])
				assert.Equal(t, "2048Mi", resourceLimits["requestsMemory"])
			},
		},
		"create project plan minimal": {
			params: createProjectParams{
				Cluster: "local",
				Name:    "minimal-project",
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				assert.Equal(t, response.OperationCreate, planResource.Type)
				assert.Equal(t, "minimal-project", planResource.Resource.Name)

				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "local", spec["clusterName"])

				// Empty resource limits should still be present
				_, ok = spec["containerDefaultResourceLimit"]
				assert.True(t, ok)
			},
		},
		"create project plan with namespace default resource quotas": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "project-with-ns-quotas",
				DisplayName: "Project with Namespace Quotas",
				NamespaceDefaultResourceQuota: resourceQuota{
					Pods:                   100,
					Services:               50,
					Secrets:                100,
					ConfigMaps:             50,
					PersistentVolumeClaims: 10,
					RequestsCPU:            5000,
					RequestsMemory:         10240,
				},
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)

				nsQuota, ok := spec["namespaceDefaultResourceQuota"].(map[string]any)
				require.True(t, ok)
				limit, ok := nsQuota["limit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "100", limit["pods"])
				assert.Equal(t, "50", limit["services"])
				assert.Equal(t, "100", limit["secrets"])
				assert.Equal(t, "50", limit["configMaps"])
				assert.Equal(t, "10", limit["persistentVolumeClaims"])
				assert.Equal(t, "5000m", limit["requestsCpu"])
				assert.Equal(t, "10240Mi", limit["requestsMemory"])
			},
		},
		"create project plan with resource quotas": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "project-with-quotas",
				DisplayName: "Project with Resource Quotas",
				ResourceQuota: resourceQuota{
					ReplicationControllers: 20,
					ServicesNodePorts:      5,
					ServicesLoadBalancers:  3,
					LimitsCPU:              10000,
					LimitsMemory:           20480,
					RequestsStorage:        51200,
				},
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)

				resQuota, ok := spec["resourceQuota"].(map[string]any)
				require.True(t, ok)
				limit, ok := resQuota["limit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "20", limit["replicationControllers"])
				assert.Equal(t, "5", limit["servicesNodePorts"])
				assert.Equal(t, "3", limit["servicesLoadBalancers"])
				assert.Equal(t, "10000m", limit["limitsCpu"])
				assert.Equal(t, "20480Mi", limit["limitsMemory"])
				assert.Equal(t, "51200Mi", limit["requestsStorage"])
			},
		},
		"create project plan with all fields": {
			params: createProjectParams{
				Cluster:     "local",
				Name:        "comprehensive-project",
				DisplayName: "Comprehensive Project",
				Description: "A project with all fields set",
				ContainerDefaultResourceLimit: containerDefaultResourceLimit{
					CPULimit:          1500,
					CPUReservation:    500,
					MemoryLimit:       2048,
					MemoryReservation: 1024,
				},
				NamespaceDefaultResourceQuota: resourceQuota{
					Pods:     50,
					Services: 25,
				},
				ResourceQuota: resourceQuota{
					ServicesNodePorts: 10,
					LimitsCPU:         8000,
				},
			},
			validateResult: func(t *testing.T, result string) {
				var planResources []response.PlanResource
				err := json.Unmarshal([]byte(result), &planResources)
				require.NoError(t, err)
				require.Len(t, planResources, 1)

				planResource := planResources[0]
				payload, ok := planResource.Payload.(map[string]any)
				require.True(t, ok)

				spec, ok := payload["spec"].(map[string]any)
				require.True(t, ok)

				// Validate container default resource limits
				containerLimits, ok := spec["containerDefaultResourceLimit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "1500m", containerLimits["limitsCpu"])
				assert.Equal(t, "500m", containerLimits["requestsCpu"])
				assert.Equal(t, "2048Mi", containerLimits["limitsMemory"])
				assert.Equal(t, "1024Mi", containerLimits["requestsMemory"])

				// Validate namespace default resource quotas
				nsQuota, ok := spec["namespaceDefaultResourceQuota"].(map[string]any)
				require.True(t, ok)
				nsLimit, ok := nsQuota["limit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "50", nsLimit["pods"])
				assert.Equal(t, "25", nsLimit["services"])

				// Validate resource quotas
				resQuota, ok := spec["resourceQuota"].(map[string]any)
				require.True(t, ok)
				resLimit, ok := resQuota["limit"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "10", resLimit["servicesNodePorts"])
				assert.Equal(t, "8000m", resLimit["limitsCpu"])
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{}
			tools := NewTools(test.WrapClient(c, fakeToken, fakeUrl), "")
			req := test.NewCallToolRequest(fakeUrl)

			result, _, err := tools.createProjectPlan(context.Background(), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, result.Content, 1)

				textContent, ok := result.Content[0].(*mcp.TextContent)
				require.True(t, ok)

				if tt.validateResult != nil {
					tt.validateResult(t, textContent.Text)
				}
			}
		})
	}
}
