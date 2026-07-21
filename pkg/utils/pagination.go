package utils

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const defaultListLimit int64 = 100

// PaginationResult holds the windowed items and pagination metadata.
type PaginationResult struct {
	Items   []*unstructured.Unstructured
	Total   int64
	Offset  int64
	Limit   int64
	HasMore bool
}

// Paginator sorts and paginates a slice of unstructured Kubernetes resources.
type Paginator interface {
	SortAndPaginate(resources []*unstructured.Unstructured, offset, limit int64) PaginationResult
	BuildNote(result PaginationResult, filterSuffix string) string
}

// ResourcePaginator implements Paginator for Kubernetes unstructured resources.
type ResourcePaginator struct{}

// NewResourcePaginator returns a new ResourcePaginator.
func NewResourcePaginator() *ResourcePaginator {
	return &ResourcePaginator{}
}

// SortAndPaginate sorts resources by namespace/name and returns the requested
// offset/limit window. Limit <= 0 defaults to DefaultListLimit; negative
// offset is clamped to 0.
func (p *ResourcePaginator) SortAndPaginate(resources []*unstructured.Unstructured, offset, limit int64) PaginationResult {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}

	sort.Slice(resources, func(i, j int) bool {
		if ns := resources[i].GetNamespace(); ns != resources[j].GetNamespace() {
			return ns < resources[j].GetNamespace()
		}
		return resources[i].GetName() < resources[j].GetName()
	})

	total := int64(len(resources))
	start := min(offset, total)
	end := min(offset+limit, total)

	return PaginationResult{
		Items:   resources[start:end],
		Total:   total,
		Offset:  offset,
		Limit:   limit,
		HasMore: offset+limit < total,
	}
}

// BuildNote returns a human-readable pagination note. It returns an empty
// string when there is no pagination context (single page, offset 0).
func (p *ResourcePaginator) BuildNote(result PaginationResult, filterSuffix string) string {
	if !result.HasMore && result.Offset == 0 {
		return ""
	}

	note := fmt.Sprintf("Returned %d resources (offset %d, limit %d) out of %d total%s. "+
		"Use a namespace or label selector to narrow results, or increase the limit.",
		len(result.Items), result.Offset, result.Limit, result.Total, filterSuffix)
	if result.HasMore {
		note += fmt.Sprintf(" To get the next page, set offset=%d.", result.Offset+result.Limit)
	}

	return note
}
