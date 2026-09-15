package projects

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/rancher-ai-mcp/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// getProjectID retrieves the project ID for a given project name or ID.
func GetProjectID(ctx context.Context, tClient toolsClient, token, clusterID, projectNameOrID string) (string, *unstructured.Unstructured, error) {
	// Check if the project was given by ID
	projectResource, err := tClient.GetResource(ctx, client.GetParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: clusterID,
		Name:      projectNameOrID,
		Token:     token,
	})
	if err == nil {
		return projectResource.GetName(), projectResource, nil
	}

	if !apierrors.IsNotFound(err) {
		return "", nil, err
	}

	// Search for project by DisplayName
	resources, err := tClient.GetResources(ctx, client.ListParams{
		Cluster:   LocalCluster,
		Kind:      "project",
		Namespace: clusterID,
		Token:     token,
	})
	if err != nil {
		return "", nil, err
	}

	for _, resource := range resources {
		displayName, found, err := unstructured.NestedString(resource.Object, "spec", "displayName")
		if err != nil || !found {
			continue
		}

		if strings.EqualFold(displayName, projectNameOrID) {
			return resource.GetName(), resource, nil
		}
	}

	return "", nil, fmt.Errorf("project '%s' not found in cluster '%s'", projectNameOrID, clusterID)
}

// Get the project backing namespace of a project. It is either <clusterID-projectID> or just <projectID>.
// It is specified in the field "status.backingNamespace" but defaults to the name if that field isn't present.
func GetProjectBackingNamespace(project *unstructured.Unstructured) (string, error) {
	projectBackingNamespace, found, err := unstructured.NestedString(project.Object, "status", "backingNamespace")
	if err != nil {
		return "", err
	}
	if !found {
		projectBackingNamespace, found, err = unstructured.NestedString(project.Object, "metadata", "name")
		if err != nil || !found {
			return "", fmt.Errorf("failed to get backing namespace for project: %w", err)
		}
	}
	return projectBackingNamespace, nil
}
