package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func makePod(name, namespace string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetName(name)
	u.SetNamespace(namespace)
	return u
}

func TestSortAndPaginate(t *testing.T) {
	p := NewResourcePaginator()

	tests := map[string]struct {
		resources     []*unstructured.Unstructured
		offset        int64
		limit         int64
		expectedNames []string
		expectedTotal int64
		hasMore       bool
		resultOffset  int64
		resultLimit   int64
	}{
		"default limit when limit is 0": {
			resources:     []*unstructured.Unstructured{makePod("a", "ns")},
			offset:        0,
			limit:         0,
			expectedNames: []string{"a"},
			expectedTotal: 1,
			hasMore:       false,
			resultOffset:  0,
			resultLimit:   defaultListLimit,
		},
		"negative offset clamped to 0": {
			resources:     []*unstructured.Unstructured{makePod("a", "ns")},
			offset:        -5,
			limit:         10,
			expectedNames: []string{"a"},
			expectedTotal: 1,
			hasMore:       false,
			resultOffset:  0,
			resultLimit:   10,
		},
		"first page with hasMore": {
			resources: []*unstructured.Unstructured{
				makePod("c", "ns"),
				makePod("a", "ns"),
				makePod("b", "ns"),
			},
			offset:        0,
			limit:         2,
			expectedNames: []string{"a", "b"},
			expectedTotal: 3,
			hasMore:       true,
			resultOffset:  0,
			resultLimit:   2,
		},
		"second page no hasMore": {
			resources: []*unstructured.Unstructured{
				makePod("c", "ns"),
				makePod("a", "ns"),
				makePod("b", "ns"),
			},
			offset:        2,
			limit:         2,
			expectedNames: []string{"c"},
			expectedTotal: 3,
			hasMore:       false,
			resultOffset:  2,
			resultLimit:   2,
		},
		"offset beyond total": {
			resources: []*unstructured.Unstructured{
				makePod("a", "ns"),
				makePod("b", "ns"),
			},
			offset:        10,
			limit:         5,
			expectedNames: []string{},
			expectedTotal: 2,
			hasMore:       false,
			resultOffset:  10,
			resultLimit:   5,
		},
		"sorted by namespace then name": {
			resources: []*unstructured.Unstructured{
				makePod("pod-1", "bravo"),
				makePod("pod-2", "alpha"),
				makePod("pod-1", "alpha"),
			},
			offset:        0,
			limit:         10,
			expectedNames: []string{"pod-1", "pod-2", "pod-1"},
			expectedTotal: 3,
			hasMore:       false,
			resultOffset:  0,
			resultLimit:   10,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := p.SortAndPaginate(tt.resources, tt.offset, tt.limit)

			names := make([]string, len(result.Items))
			for i, item := range result.Items {
				names[i] = item.GetName()
			}

			assert.Equal(t, tt.expectedNames, names)
			assert.Equal(t, tt.expectedTotal, result.Total)
			assert.Equal(t, tt.hasMore, result.HasMore)
			assert.Equal(t, tt.resultOffset, result.Offset)
			assert.Equal(t, tt.resultLimit, result.Limit)
		})
	}
}

func TestBuildNote(t *testing.T) {
	p := NewResourcePaginator()

	tests := map[string]struct {
		result       PaginationResult
		filterSuffix string
		expected     string
	}{
		"empty when no pagination context": {
			result: PaginationResult{
				Items:   []*unstructured.Unstructured{makePod("a", "ns")},
				Total:   1,
				Offset:  0,
				Limit:   10,
				HasMore: false,
			},
			expected: "",
		},
		"includes next page when hasMore": {
			result: PaginationResult{
				Items:   []*unstructured.Unstructured{makePod("a", "ns")},
				Total:   3,
				Offset:  0,
				Limit:   1,
				HasMore: true,
			},
			expected: "Returned 1 resources (offset 0, limit 1) out of 3 total. " +
				"Use a namespace or label selector to narrow results, or increase the limit. " +
				"To get the next page, set offset=1.",
		},
		"no next page hint on last page with offset": {
			result: PaginationResult{
				Items:   []*unstructured.Unstructured{makePod("a", "ns")},
				Total:   2,
				Offset:  1,
				Limit:   1,
				HasMore: false,
			},
			expected: "Returned 1 resources (offset 1, limit 1) out of 2 total. " +
				"Use a namespace or label selector to narrow results, or increase the limit.",
		},
		"includes filterSuffix": {
			result: PaginationResult{
				Items:   []*unstructured.Unstructured{makePod("a", "ns")},
				Total:   3,
				Offset:  0,
				Limit:   1,
				HasMore: true,
			},
			filterSuffix: " matching the JSONPath filter",
			expected: "Returned 1 resources (offset 0, limit 1) out of 3 total matching the JSONPath filter. " +
				"Use a namespace or label selector to narrow results, or increase the limit. " +
				"To get the next page, set offset=1.",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			note := p.BuildNote(tt.result, tt.filterSuffix)
			assert.Equal(t, tt.expected, note)
		})
	}
}
