package rbac

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

const existingBindingText = "role 'project-owner' is already granted in project 'local:p-web' by project role template binding 'local-p-web/prtb-existing', no changes needed"

func newMgmtObject(kind, namespace, name string, fields map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: fields}
	obj.SetAPIVersion("management.cattle.io/v3")
	obj.SetKind(kind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func newPRTBTestTools(extraObjects ...runtime.Object) (*Tools, *dynamicfake.FakeDynamicClient) {
	objects := []runtime.Object{
		newMgmtObject("Project", "local", "p-web", map[string]any{
			"spec":   map[string]any{"displayName": "web"},
			"status": map[string]any{"backingNamespace": "local-p-web"},
		}),
		newMgmtObject("User", "", "u-alice", map[string]any{"username": "alice", "principalIds": []any{"local://u-alice"}}),
		newMgmtObject("RoleTemplate", "", "project-member", map[string]any{"context": "project"}),
		newMgmtObject("RoleTemplate", "", "project-owner", map[string]any{"context": "project"}),
		newMgmtObject("RoleTemplate", "", "cluster-owner", map[string]any{"context": "cluster"}),
		newMgmtObject("RoleTemplate", "", "locked-role", map[string]any{"context": "project", "locked": true}),
		newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-existing", map[string]any{
			"projectName": "local:p-web", "roleTemplateName": "project-owner", "userName": "u-alice",
		}),
	}
	objects = append(objects, extraObjects...)
	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, objects...)
	c := &client.Client{
		DynClientCreator: func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil },
	}
	return NewTools(test.WrapClient(c, fakeToken), false), fakeDynClient
}

func TestCreateProjectRoleTemplateBinding(t *testing.T) {
	tests := map[string]struct {
		params         createPRTBParams
		extraObjects   []runtime.Object
		expectedFields map[string]any
		expectedResult string
		expectedError  string
	}{
		"grant default role to user by username and project by display name": {
			params:         createPRTBParams{Cluster: "local", Project: "web", User: "alice"},
			expectedFields: map[string]any{"projectName": "local:p-web", "roleTemplateName": "project-member", "userName": "u-alice"},
		},
		"grant role to group by project ID": {
			params:         createPRTBParams{Cluster: "local", Project: "p-web", Group: "github_team://123", RoleTemplate: "project-member"},
			expectedFields: map[string]any{"projectName": "local:p-web", "roleTemplateName": "project-member", "groupPrincipalName": "github_team://123"},
		},
		"existing binding is not duplicated": {
			params:         createPRTBParams{Cluster: "local", Project: "web", User: "u-alice", RoleTemplate: "project-owner"},
			expectedResult: existingBindingText,
		},
		"existing binding by user principal is not duplicated": {
			params: createPRTBParams{Cluster: "local", Project: "web", User: "alice"},
			extraObjects: []runtime.Object{newMgmtObject("ProjectRoleTemplateBinding", "local-p-web", "prtb-principal", map[string]any{
				"projectName": "local:p-web", "roleTemplateName": "project-member", "userPrincipalName": "local://u-alice",
			})},
			expectedResult: "role 'project-member' is already granted in project 'local:p-web' by project role template binding 'local-p-web/prtb-principal', no changes needed",
		},
		"user and group both specified": {
			params:        createPRTBParams{Cluster: "local", Project: "web", User: "alice", Group: "github_team://123"},
			expectedError: "exactly one of user or group must be specified",
		},
		"neither user nor group specified": {
			params:        createPRTBParams{Cluster: "local", Project: "web"},
			expectedError: "exactly one of user or group must be specified",
		},
		"unknown user": {
			params:        createPRTBParams{Cluster: "local", Project: "web", User: "bob"},
			expectedError: "user 'bob' not found",
		},
		"unknown project": {
			params:        createPRTBParams{Cluster: "local", Project: "api", User: "alice"},
			expectedError: "project 'api' not found in cluster 'local'",
		},
		"unknown role template": {
			params:        createPRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "nonexistent"},
			expectedError: "role template 'nonexistent' not found",
		},
		"cluster role template": {
			params:        createPRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "cluster-owner"},
			expectedError: "role template 'cluster-owner' is not a project role template (context is 'cluster')",
		},
		"locked role template": {
			params:        createPRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "locked-role"},
			expectedError: "role template 'locked-role' is locked and cannot be assigned",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newPRTBTestTools(tt.extraObjects...)

			result, _, err := tools.createProjectRoleTemplateBinding(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}
			require.NoError(t, err)
			text := result.Content[0].(*mcp.TextContent).Text
			if tt.expectedResult != "" {
				assert.Equal(t, tt.expectedResult, text)
				return
			}

			subject := tt.params.Group
			if subject == "" {
				subject = "u-alice"
			}
			expectedName := prtbName("local:p-web", tt.expectedFields["roleTemplateName"].(string), subject)
			assert.Contains(t, text, expectedName)

			created, err := fakeDynClient.Resource(converter.K8sKindsToGVRs["projectroletemplatebinding"]).
				Namespace("local-p-web").Get(t.Context(), expectedName, metav1.GetOptions{})
			require.NoError(t, err, "PRTB was not created")
			for field, value := range tt.expectedFields {
				assert.Equal(t, value, created.Object[field], field)
			}
		})
	}
}

func TestCreateProjectRoleTemplateBindingPlan(t *testing.T) {
	name := prtbName("local:p-web", "project-member", "u-alice")
	tests := map[string]struct {
		params         createPRTBParams
		expectedResult string
	}{
		"plan granting default role to user": {
			params: createPRTBParams{Cluster: "local", Project: "web", User: "alice"},
			expectedResult: fmt.Sprintf(`[{
				"type": "create",
				"resource": {"name": %[1]q, "kind": "ProjectRoleTemplateBinding", "cluster": "local", "namespace": "local-p-web"},
				"payload": {
					"apiVersion": "management.cattle.io/v3",
					"kind": "ProjectRoleTemplateBinding",
					"metadata": {"name": %[1]q, "namespace": "local-p-web"},
					"projectName": "local:p-web",
					"roleTemplateName": "project-member",
					"userName": "u-alice"
				}
			}]`, name),
		},
		"plan for existing binding": {
			params:         createPRTBParams{Cluster: "local", Project: "web", User: "alice", RoleTemplate: "project-owner"},
			expectedResult: existingBindingText,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tools, fakeDynClient := newPRTBTestTools()

			result, _, err := tools.createProjectRoleTemplateBindingPlan(
				middleware.WithToken(t.Context(), fakeToken),
				test.NewCallToolRequest(fakeURL),
				tt.params,
			)

			require.NoError(t, err)
			text := result.Content[0].(*mcp.TextContent).Text
			if tt.expectedResult == existingBindingText {
				assert.Equal(t, tt.expectedResult, text)
			} else {
				assert.JSONEq(t, tt.expectedResult, text)
			}

			prtbs, err := fakeDynClient.Resource(converter.K8sKindsToGVRs["projectroletemplatebinding"]).
				Namespace("local-p-web").List(t.Context(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, prtbs.Items, 1, "plan must not create resources")
		})
	}
}
