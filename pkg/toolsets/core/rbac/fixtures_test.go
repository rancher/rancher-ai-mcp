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
// For responses without resources, such as "no resources found", Note holds the message, unless the
// response also has a note.
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

	var summary resultSummary
	resources := resp.LLM
	var withNote struct {
		Resources json.RawMessage `json:"resources"`
		Note      string          `json:"note"`
	}
	if err := json.Unmarshal(resp.LLM, &withNote); err == nil && withNote.Resources != nil {
		resources = withNote.Resources
		summary.Note = withNote.Note
	}

	var message string
	if err := json.Unmarshal(resources, &message); err == nil {
		if summary.Note == "" {
			summary.Note = message
		}
		return summary
	}

	var list []*unstructured.Unstructured
	var objs []map[string]any
	require.NoError(t, json.Unmarshal(resources, &objs))
	for _, obj := range objs {
		list = append(list, &unstructured.Unstructured{Object: obj})
	}
	for _, r := range list {
		summary.Names = append(summary.Names, r.GetName())
	}

	return summary
}
