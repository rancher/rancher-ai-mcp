package rbac

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func filterResourcesByField(resources []*unstructured.Unstructured, field, value string) []*unstructured.Unstructured {
	var filteredResources []*unstructured.Unstructured
	for _, resource := range resources {
		resourceValue, found, err := unstructured.NestedString(resource.Object, field)
		if err == nil && found && resourceValue == value {
			filteredResources = append(filteredResources, resource)
		}
	}
	return filteredResources
}
