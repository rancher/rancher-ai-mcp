package utils

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"
)

// FilterByJSONPath returns the subset of objs matching the given JSONPath predicate
// expression (the body of a kubectl-style [?(...)] filter, e.g. `@.status.phase=="Running"`).
// The objects are wrapped as a list so the filter can iterate over them, mirroring
// kubectl's `{.items[?(<predicate>)]}` selector semantics.
func FilterByJSONPath(objs []*unstructured.Unstructured, expr string) ([]*unstructured.Unstructured, error) {
	items := make([]interface{}, len(objs))
	for i, obj := range objs {
		items[i] = obj.Object
	}
	input := map[string]interface{}{"items": items}

	jp := jsonpath.New("filter").AllowMissingKeys(true)
	if err := jp.Parse("{.items[?(" + expr + ")]}"); err != nil {
		return nil, fmt.Errorf("invalid jsonPath filter %q: %w", expr, err)
	}

	results, err := jp.FindResults(input)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate jsonPath filter %q: %w", expr, err)
	}

	filtered := make([]*unstructured.Unstructured, 0)
	for _, group := range results {
		for _, v := range group {
			if m, ok := v.Interface().(map[string]interface{}); ok {
				filtered = append(filtered, &unstructured.Unstructured{Object: m})
			}
		}
	}

	return filtered, nil
}
