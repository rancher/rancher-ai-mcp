package rbac

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// withServerFields adds the fields that the API server and Rancher's controllers maintain on a
// resource, which should be removed from responses, and returns the resource. The creator annotation
// and the status summary are kept in responses.
func withServerFields(obj *unstructured.Unstructured) *unstructured.Unstructured {
	obj.SetUID("5f4bd8fc-80a6-4567-ad76-9706818d6a48")
	obj.SetResourceVersion("133639")
	obj.SetGeneration(2)
	obj.SetGenerateName("grb-")
	obj.SetFinalizers([]string{"controller.cattle.io/mgmt-auth-grb-controller"})
	obj.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "management.cattle.io/v3", Kind: "GlobalRole", Name: "admin", UID: "9ee2d536"}})
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "rancher", Operation: metav1.ManagedFieldsOperationUpdate}})
	obj.SetAnnotations(map[string]string{
		"authz.management.cattle.io/crb-name":                 "cattle-globalrolebinding-grb-1",
		"cleanup.cattle.io/grbUpgradeCluster":                 "true",
		"lifecycle.cattle.io/create.mgmt-auth-grb-controller": "true",
		"field.cattle.io/creatorId":                           "user-sdxtj",
	})
	obj.Object["status"] = map[string]any{
		"lastUpdateTime":          "2026-11-04T14:38:09Z",
		"observedGenerationLocal": int64(1),
		"summary":                 "Completed",
		"localConditions": []any{
			map[string]any{"type": "SubjectReconciled", "status": "True", "reason": "SubjectExists", "lastTransitionTime": "2026-11-04T14:38:09Z"},
		},
	}

	return obj
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

	var (
		list []*unstructured.Unstructured
		objs []map[string]any
	)
	require.NoError(t, json.Unmarshal(resources, &objs))

	for _, obj := range objs {
		list = append(list, &unstructured.Unstructured{Object: obj})
	}

	for _, r := range list {
		summary.Names = append(summary.Names, r.GetName())
	}

	return summary
}
