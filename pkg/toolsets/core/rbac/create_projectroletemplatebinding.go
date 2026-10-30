package rbac

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/toolsets/core/projects"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const defaultProjectRoleTemplate = "project-member"

var (
	zapCreateProjectRoleTemplateBinding     = zap.String("tool", "createProjectRoleTemplateBinding")
	zapCreateProjectRoleTemplateBindingPlan = zap.String("tool", "createProjectRoleTemplateBindingPlan")
)

type createPRTBParams struct {
	Cluster      string `json:"cluster" jsonschema:"the name or ID of the cluster the project belongs to"`
	Project      string `json:"project" jsonschema:"the ID (e.g. p-abc) or display name of the project"`
	User         string `json:"user,omitempty" jsonschema:"the username or user ID to grant access to. Exactly one of user or group must be specified"`
	Group        string `json:"group,omitempty" jsonschema:"the group principal ID to grant access to (e.g. github_team://123). Exactly one of user or group must be specified"`
	RoleTemplate string `json:"roleTemplate,omitempty" jsonschema:"the name of the project role template to grant (e.g. project-owner, project-member, read-only). Defaults to project-member"`
}

// projectRoleTemplateBinding holds a PRTB to be created, or an existing PRTB
// that already grants the requested access.
type projectRoleTemplateBinding struct {
	prtb     *unstructured.Unstructured
	existing *unstructured.Unstructured
}

// createProjectRoleTemplateBinding grants a user or group a role in a project.
func (t *Tools) createProjectRoleTemplateBinding(ctx context.Context, toolReq *mcp.CallToolRequest, params createPRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createProjectRoleTemplateBinding called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	binding, err := t.buildPRTB(ctx, params)
	if err != nil {
		zap.L().Error("failed to build project role template binding", zapCreateProjectRoleTemplateBinding, zap.Error(err))
		return nil, nil, err
	}
	if binding.existing != nil {
		return existingBindingResult(binding.existing), nil, nil
	}

	resourceInterface, err := t.client.GetResourceInterface(ctx, middleware.Token(ctx), binding.prtb.GetNamespace(), "local", converter.K8sKindsToGVRs["projectroletemplatebinding"])
	if err != nil {
		return nil, nil, err
	}

	obj, err := resourceInterface.Create(ctx, binding.prtb, metav1.CreateOptions{})
	if err != nil {
		zap.L().Error("failed to create project role template binding", zapCreateProjectRoleTemplateBinding, zap.Error(err))
		return nil, nil, fmt.Errorf("failed to create project role template binding: %w", err)
	}

	mcpResponse, err := response.CreateMcpResponse([]*unstructured.Unstructured{obj}, "local")
	if err != nil {
		zap.L().Error("failed to create mcp response", zapCreateProjectRoleTemplateBinding, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// createProjectRoleTemplateBindingPlan plans granting a user or group a role in a project.
// It returns the JSON representation of the PRTB to be created without actually creating it.
func (t *Tools) createProjectRoleTemplateBindingPlan(ctx context.Context, toolReq *mcp.CallToolRequest, params createPRTBParams) (*mcp.CallToolResult, any, error) {
	zap.L().Debug("createProjectRoleTemplateBindingPlan called", zap.String("cluster", params.Cluster), zap.String("project", params.Project))

	binding, err := t.buildPRTB(ctx, params)
	if err != nil {
		zap.L().Error("failed to build project role template binding", zapCreateProjectRoleTemplateBindingPlan, zap.Error(err))
		return nil, nil, err
	}
	if binding.existing != nil {
		return existingBindingResult(binding.existing), nil, nil
	}

	createResource := response.NewCreateResourceInput(binding.prtb, "local")
	mcpResponse, err := response.CreatePlanResponse([]response.PlanResource{createResource})
	if err != nil {
		zap.L().Error("failed to create plan response", zapCreateProjectRoleTemplateBindingPlan, zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// buildPRTB resolves the cluster, project, subject and role template and builds the PRTB granting the access.
// If a PRTB already grants the same role to the same subject in the project, it is returned as existing instead.
func (t *Tools) buildPRTB(ctx context.Context, params createPRTBParams) (*projectRoleTemplateBinding, error) {
	target, err := t.resolvePRTBTarget(ctx, params)
	if err != nil {
		return nil, err
	}

	if err := t.validateProjectRoleTemplate(ctx, target.roleTemplateName); err != nil {
		return nil, err
	}

	prtbs, err := t.findPRTBs(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(prtbs) > 0 {
		return &projectRoleTemplateBinding{existing: prtbs[0]}, nil
	}

	prtb := &unstructured.Unstructured{
		Object: map[string]any{
			"projectName":       target.projectName,
			"roleTemplateName":  target.roleTemplateName,
			target.subjectField: target.subject,
		},
	}
	prtb.SetKind("ProjectRoleTemplateBinding")
	prtb.SetAPIVersion(converter.ManagementGroup + "/v3")
	prtb.SetName(prtbName(target.projectName, target.roleTemplateName, target.subject))
	prtb.SetNamespace(target.namespace)

	return &projectRoleTemplateBinding{prtb: prtb}, nil
}

// prtbTarget identifies the role granted to a subject in a project by a PRTB.
type prtbTarget struct {
	namespace        string
	projectName      string
	roleTemplateName string
	subjectField     string
	subject          string
	// subjectIdentities maps each PRTB field that can identify the subject to the values that match it.
	subjectIdentities map[string][]string
}

// resolvePRTBTarget resolves the cluster, project and subject from the tool parameters.
func (t *Tools) resolvePRTBTarget(ctx context.Context, params createPRTBParams) (*prtbTarget, error) {
	if (params.User == "") == (params.Group == "") {
		return nil, errors.New("exactly one of user or group must be specified")
	}
	roleTemplateName := params.RoleTemplate
	if roleTemplateName == "" {
		roleTemplateName = defaultProjectRoleTemplate
	}
	token := middleware.Token(ctx)

	clusterID, err := t.client.GetClusterID(ctx, token, params.Cluster)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve cluster ID: %w", err)
	}

	projectID, projectResource, err := projects.GetProjectID(ctx, t.client, token, clusterID, params.Project)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	namespace, err := projects.GetProjectBackingNamespace(projectResource)
	if err != nil {
		return nil, fmt.Errorf("failed to get project backing namespace: %w", err)
	}

	subjectField, subject := "groupPrincipalName", params.Group
	subjectIdentities := map[string][]string{
		"groupPrincipalName": {params.Group},
		"groupName":          {params.Group},
	}
	if params.User != "" {
		users, err := t.findUser(ctx, params.User)
		if err != nil {
			return nil, fmt.Errorf("failed to get user: %w", err)
		}
		if len(users) == 0 {
			return nil, fmt.Errorf("user '%s' not found", params.User)
		}
		subjectField, subject = "userName", users[0].GetName()
		principalIDs, _, _ := unstructured.NestedStringSlice(users[0].Object, "principalIds")
		subjectIdentities = map[string][]string{
			"userName":          {subject},
			"userPrincipalName": principalIDs,
		}
	}

	return &prtbTarget{
		namespace:         namespace,
		projectName:       clusterID + ":" + projectID,
		roleTemplateName:  roleTemplateName,
		subjectField:      subjectField,
		subject:           subject,
		subjectIdentities: subjectIdentities,
	}, nil
}

// findPRTBs returns the active PRTBs granting the target role to the target subject in the project.
func (t *Tools) findPRTBs(ctx context.Context, target *prtbTarget) ([]*unstructured.Unstructured, error) {
	prtbs, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   "local",
		Kind:      "projectroletemplatebinding",
		Namespace: target.namespace,
		Token:     middleware.Token(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get project role template bindings: %w", err)
	}
	prtbs = filterResourcesByField(prtbs, "projectName", target.projectName)
	prtbs = filterResourcesByField(prtbs, "roleTemplateName", target.roleTemplateName)

	// PRTBs are kept until Rancher's finalizers have run, ignore those that are already being deleted.
	var active []*unstructured.Unstructured
	for _, prtb := range prtbs {
		if prtb.GetDeletionTimestamp() == nil && target.matchesSubject(prtb) {
			active = append(active, prtb)
		}
	}
	return active, nil
}

// matchesSubject returns true if the PRTB identifies the target subject by any of its identity fields,
// e.g. a user binding may only set userPrincipalName rather than userName.
func (target *prtbTarget) matchesSubject(prtb *unstructured.Unstructured) bool {
	for field, values := range target.subjectIdentities {
		value, _, _ := unstructured.NestedString(prtb.Object, field)
		if value != "" && slices.Contains(values, value) {
			return true
		}
	}
	return false
}

// validateProjectRoleTemplate checks that the role template exists, is a project role and is not locked.
func (t *Tools) validateProjectRoleTemplate(ctx context.Context, name string) error {
	roleTemplate, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: "local",
		Kind:    "roletemplate",
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("role template '%s' not found", name)
		}
		return fmt.Errorf("failed to get role template: %w", err)
	}

	if roleContext, _, _ := unstructured.NestedString(roleTemplate.Object, "context"); roleContext != "project" {
		return fmt.Errorf("role template '%s' is not a project role template (context is '%s')", name, roleContext)
	}
	if locked, _, _ := unstructured.NestedBool(roleTemplate.Object, "locked"); locked {
		return fmt.Errorf("role template '%s' is locked and cannot be assigned", name)
	}

	return nil
}

// prtbName generates a deterministic name for a PRTB so that the planned and created resources match,
// and creating the same binding twice is rejected by the API.
func prtbName(projectName, roleTemplateName, subject string) string {
	sum := sha256.Sum256([]byte(projectName + "/" + roleTemplateName + "/" + subject))
	return "prtb-" + hex.EncodeToString(sum[:])[:12]
}

func existingBindingResult(prtb *unstructured.Unstructured) *mcp.CallToolResult {
	roleTemplateName, _, _ := unstructured.NestedString(prtb.Object, "roleTemplateName")
	projectName, _, _ := unstructured.NestedString(prtb.Object, "projectName")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("role '%s' is already granted in project '%s' by project role template binding '%s/%s', no changes needed",
				roleTemplateName, projectName, prtb.GetNamespace(), prtb.GetName()),
		}},
	}
}
