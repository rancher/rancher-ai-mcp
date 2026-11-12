package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
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

// testNow is the time the tools under test think it is.
var testNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// testCreated is when test users are created, unless a test says otherwise.
var testCreated = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// daysAgo returns the time the given number of days before testNow.
func daysAgo(days int) time.Time { return testNow.AddDate(0, 0, -days) }

// rfc3339 formats t as Rancher does in token fields.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// objectOption customises an object built by a fixture function.
type objectOption func(*unstructured.Unstructured)

// createdAt sets the creation time of the object.
func createdAt(t time.Time) objectOption {
	return func(u *unstructured.Unstructured) { u.SetCreationTimestamp(metav1.NewTime(t)) }
}

// withLabel adds a label to the object.
func withLabel(key, value string) objectOption {
	return func(u *unstructured.Unstructured) {
		labels := u.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}

		labels[key] = value
		u.SetLabels(labels)
	}
}

// lastLoginAt sets the user's last-login label to t.
func lastLoginAt(t time.Time) objectOption {
	return withLabel(lastLoginLabel, strconv.FormatInt(t.Unix(), 10))
}

// user returns a User created at testCreated, with the login name username unless it is empty, as for
// users created by an external provider.
func user(name, username string, fields map[string]any, opts ...objectOption) *unstructured.Unstructured {
	fields = maps.Clone(fields)
	if fields == nil {
		fields = map[string]any{}
	}

	if username != "" {
		fields["username"] = username
	}

	u := managementObject("User", name, "", fields)
	u.SetCreationTimestamp(metav1.NewTime(testCreated))

	for _, opt := range opts {
		opt(u)
	}

	return u
}

// userAttribute returns the UserAttribute of a user, recording the username they log in with at each
// provider and the group principals they belong to, e.g. openldap_group://cn=ops or local://g-abc.
func userAttribute(userID string, usernames map[string]string, groups ...string) *unstructured.Unstructured {
	extra := map[string]any{}
	for provider, username := range usernames {
		extra[provider] = map[string]any{"username": []any{username}}
	}

	groupPrincipals := map[string]any{}

	for _, group := range groups {
		scheme, _, _ := strings.Cut(group, "://")
		provider := strings.TrimSuffix(scheme, "_group")

		providerMap, ok := groupPrincipals[provider].(map[string]any)
		if !ok {
			providerMap = map[string]any{"Items": []any{}}
			groupPrincipals[provider] = providerMap
		}

		providerMap["Items"] = append(providerMap["Items"].([]any), map[string]any{"metadata": map[string]any{"name": group}})
	}

	return managementObject("UserAttribute", userID, "", map[string]any{"ExtraByProvider": extra, "GroupPrincipals": groupPrincipals})
}

func v3Token(name string, labels map[string]string, fields map[string]any) *unstructured.Unstructured {
	u := managementObject("Token", name, "", fields)
	u.SetLabels(labels)

	return u
}

func extToken(name string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec, "status": status}}
	u.SetAPIVersion("ext.cattle.io/v1")
	u.SetKind("Token")
	u.SetName(name)

	return u
}

// fakeToolsConfig configures newFakeTools.
type fakeToolsConfig struct {
	objects []runtime.Object
	// denyListAll makes access reviews for listing every resource fail, as for callers who are not admins.
	denyListAll bool
	// accessReviewErr makes access reviews fail with an error.
	accessReviewErr error
	userInfo        authenticationv1.UserInfo
	reviewErr       error
	rancherURL      string
}

// fakeToolsOption customises the Tools returned by newFakeTools.
type fakeToolsOption func(*fakeToolsConfig)

// withObjects puts objs in the fake Kubernetes API.
func withObjects(objs ...runtime.Object) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.objects = append(c.objects, objs...) }
}

// withCaller makes the caller the user with the given ID.
func withCaller(userID string) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.userInfo = authenticationv1.UserInfo{Username: userID} }
}

// withUserInfo makes Rancher report info for the caller.
func withUserInfo(info authenticationv1.UserInfo) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.userInfo = info }
}

// withReviewError makes looking up the caller fail with err.
func withReviewError(err error) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.reviewErr = err }
}

// withoutListAll makes the caller not allowed to list every resource, so Rancher's ext token API would
// only return their own tokens.
func withoutListAll() fakeToolsOption {
	return func(c *fakeToolsConfig) { c.denyListAll = true }
}

// withAccessReviewError makes access reviews fail with err.
func withAccessReviewError(err error) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.accessReviewErr = err }
}

// withRancherURL points requests to Rancher's own APIs at url, e.g. an httptest server.
func withRancherURL(url string) fakeToolsOption {
	return func(c *fakeToolsConfig) { c.rancherURL = url }
}

// newFakeTools returns Tools backed by a fake Kubernetes API and a clientset that answers
// SelfSubjectReviews for the caller, running at testNow. It also returns the fake dynamic client so tests
// can add reactors.
func newFakeTools(t *testing.T, opts ...fakeToolsOption) (*Tools, *dynamicfake.FakeDynamicClient) {
	t.Helper()

	cfg := fakeToolsConfig{rancherURL: fakeURL}
	for _, opt := range opts {
		opt(&cfg)
	}

	fakeDynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(rbacScheme(), rbacGVRs, cfg.objects...)
	clientset := fake.NewClientset()
	clientset.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		if cfg.reviewErr != nil {
			return true, nil, cfg.reviewErr
		}

		return true, &authenticationv1.SelfSubjectReview{Status: authenticationv1.SelfSubjectReviewStatus{UserInfo: cfg.userInfo}}, nil
	})

	clientset.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		if cfg.accessReviewErr != nil {
			return true, nil, cfg.accessReviewErr
		}

		return true, &authorizationv1.SelfSubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: !cfg.denyListAll}}, nil
	})

	c, err := client.NewClientWithURL(true, cfg.rancherURL)
	require.NoError(t, err)

	c.DynClientCreator = func(_ *rest.Config) (dynamic.Interface, error) { return fakeDynClient, nil }
	c.ClientSetCreator = func(_ *rest.Config) (kubernetes.Interface, error) { return clientset, nil }

	tools := NewTools(test.WrapClient(c, fakeToken), false)
	tools.now = func() time.Time { return testNow }

	return tools, fakeDynClient
}

// forbid makes the fake client return a 403 for verb on resource.
func forbid(fakeDynClient *dynamicfake.FakeDynamicClient, verb, resource string) {
	fakeDynClient.PrependReactor(verb, resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "management.cattle.io", Resource: resource}, "", errors.New("not allowed"))
	})
}

// toolResponse is a decoded tool response.
type toolResponse[R any] struct {
	LLM       R                    `json:"llm"`
	UIContext []response.UIContext `json:"uiContext"`
	// Text is the raw response, for checking what it must never contain.
	Text string `json:"-"`
}

// callTool calls a tool handler with the test token and decodes its response into R.
func callTool[R, P any](t *testing.T, handler func(context.Context, *mcp.CallToolRequest, P) (*mcp.CallToolResult, any, error), params P) (toolResponse[R], error) {
	t.Helper()

	result, _, err := handler(middleware.WithToken(t.Context(), fakeToken), test.NewCallToolRequest(fakeURL), params)
	if err != nil {
		return toolResponse[R]{}, err
	}

	resp := toolResponse[R]{Text: result.Content[0].(*mcp.TextContent).Text}
	require.NoError(t, json.Unmarshal([]byte(resp.Text), &resp))

	return resp, nil
}

// identified is implemented by the items tools return, so tests can compare which were returned.
type identified interface{ testID() string }

func (u userSummary) testID() string    { return u.UserID }
func (u userTokenUsage) testID() string { return u.UserID }
func (u tokenUsage) testID() string     { return u.Name }

// ids returns the IDs of items, in order, and an empty slice rather than nil for no items.
func ids[T identified](items []T) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.testID())
	}

	return result
}
