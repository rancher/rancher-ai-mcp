package rbac

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// managementObject returns a management.cattle.io/v3 resource with the given top-level fields.
func managementObject(kind, name, namespace string, fields map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	maps.Copy(obj.Object, fields)
	obj.SetAPIVersion("management.cattle.io/v3")
	obj.SetKind(kind)
	obj.SetName(name)
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	return obj
}

func globalRole(name string, fields map[string]any) *unstructured.Unstructured {
	return managementObject("GlobalRole", name, "", fields)
}

// globalRoleBinding returns a binding of globalRole to the subject, where subjectField is
// userName, userPrincipalName or groupPrincipalName.
func globalRoleBinding(name, globalRole, subjectField, subject string) *unstructured.Unstructured {
	return managementObject("GlobalRoleBinding", name, "", map[string]any{
		"globalRoleName": globalRole,
		subjectField:     subject,
	})
}

// crtb returns a cluster role template binding of roleTemplate to the subject, in the cluster's namespace.
func crtb(name, cluster, roleTemplate, subjectField, subject string) *unstructured.Unstructured {
	return managementObject("ClusterRoleTemplateBinding", name, cluster, map[string]any{
		"clusterName":      cluster,
		"roleTemplateName": roleTemplate,
		subjectField:       subject,
	})
}

// prtb returns a project role template binding of roleTemplate to the subject, where projectName
// has the form <cluster>:<project>.
func prtb(name, namespace, projectName, roleTemplate, subjectField, subject string) *unstructured.Unstructured {
	return managementObject("ProjectRoleTemplateBinding", name, namespace, map[string]any{
		"projectName":      projectName,
		"roleTemplateName": roleTemplate,
		subjectField:       subject,
	})
}

func user(name, username string, fields map[string]any) *unstructured.Unstructured {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["username"] = username
	return managementObject("User", name, "", fields)
}

// resultSummary is the names of the resources in a tool response, and its note.
// For responses without resources, such as "no resources found", Note holds the message.
type resultSummary struct {
	Names []string
	Note  string
}

// summarizeResult decodes the LLM content of a tool response into a resultSummary, so tests
// can assert which resources were returned without comparing every field.
func summarizeResult(t *testing.T, result *mcp.CallToolResult) resultSummary {
	t.Helper()

	var resp struct {
		LLM json.RawMessage `json:"llm"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &resp))

	var message string
	if err := json.Unmarshal(resp.LLM, &message); err == nil {
		return resultSummary{Note: message}
	}

	var resources []*unstructured.Unstructured
	var summary resultSummary
	var withNote struct {
		Resources []map[string]any `json:"resources"`
		Note      string           `json:"note"`
	}
	if err := json.Unmarshal(resp.LLM, &withNote); err == nil && withNote.Resources != nil {
		summary.Note = withNote.Note
		for _, r := range withNote.Resources {
			resources = append(resources, &unstructured.Unstructured{Object: r})
		}
	} else {
		var list []map[string]any
		require.NoError(t, json.Unmarshal(resp.LLM, &list))
		for _, r := range list {
			resources = append(resources, &unstructured.Unstructured{Object: r})
		}
	}
	for _, r := range resources {
		summary.Names = append(summary.Names, r.GetName())
	}

	return summary
}
