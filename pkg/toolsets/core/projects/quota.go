package projects

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// extendedLimitField is the field of a Rancher resource quota limit holding extended resources, such as GPUs,
// keyed by the Kubernetes resource quota name, e.g. "requests.nvidia.com/gpu".
const extendedLimitField = "extended"

// quotaFieldToK8sName maps each field of a Rancher project resource quota limit to the matching
// resource name used by Kubernetes ResourceQuota objects.
var quotaFieldToK8sName = map[string]string{
	"pods":                   "pods",
	"services":               "services",
	"replicationControllers": "replicationcontrollers",
	"secrets":                "secrets",
	"configMaps":             "configmaps",
	"persistentVolumeClaims": "persistentvolumeclaims",
	"servicesNodePorts":      "services.nodeports",
	"servicesLoadBalancers":  "services.loadbalancers",
	"requestsCpu":            "requests.cpu",
	"requestsMemory":         "requests.memory",
	"requestsStorage":        "requests.storage",
	"limitsCpu":              "limits.cpu",
	"limitsMemory":           "limits.memory",
}

// quotaFieldNames returns the sorted names of the supported quota fields.
func quotaFieldNames() []string {
	names := make([]string, 0, len(quotaFieldToK8sName))
	for name := range quotaFieldToK8sName {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// normalizeQuotaKey converts a user supplied quota key into the key used in the flat representation of a quota:
// either the name of a known Rancher quota field or, for extended resources, a Kubernetes quota resource name.
// Known fields match case-insensitively. Extended resources contain a "/" (e.g. "nvidia.com/gpu") and are
// treated as requests unless they are already prefixed with "requests." or "limits.".
func normalizeQuotaKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	for name := range quotaFieldToK8sName {
		if strings.EqualFold(name, key) {
			return name, nil
		}
	}

	if strings.Contains(key, "/") {
		if strings.HasPrefix(key, "requests.") || strings.HasPrefix(key, "limits.") {
			return key, nil
		}

		return "requests." + key, nil
	}

	return "", fmt.Errorf("unsupported quota resource %q: use one of %s, or an extended resource such as nvidia.com/gpu",
		key, strings.Join(quotaFieldNames(), ", "))
}

// normalizeQuota validates the keys and quantities of a user supplied quota and returns it in flat form.
func normalizeQuota(quota map[string]string) (map[string]string, error) {
	flat := make(map[string]string, len(quota))
	for key, value := range quota {
		normalized, err := normalizeQuotaKey(key)
		if err != nil {
			return nil, err
		}

		value = strings.TrimSpace(value)
		if _, err := resource.ParseQuantity(value); err != nil {
			return nil, fmt.Errorf("invalid quantity %q for %q: %w", value, key, err)
		}

		if q, _ := resource.ParseQuantity(value); q.Sign() < 0 {
			return nil, fmt.Errorf("quota for %q can't be negative: %s", key, value)
		}

		flat[normalized] = value
	}

	return flat, nil
}

// flattenLimit converts the limit of a Rancher resource quota into flat form, with extended resources
// at the top level next to the known fields.
func flattenLimit(limit map[string]any) map[string]string {
	flat := make(map[string]string, len(limit))
	for key, value := range limit {
		if key == extendedLimitField {
			if extended, ok := value.(map[string]any); ok {
				for name, quantity := range extended {
					if s, ok := quantity.(string); ok {
						flat[name] = s
					}
				}
			}

			continue
		}

		if s, ok := value.(string); ok {
			flat[key] = s
		}
	}

	return flat
}

// unflattenLimit is the inverse of flattenLimit.
func unflattenLimit(flat map[string]string) map[string]any {
	limit := make(map[string]any, len(flat))
	extended := make(map[string]any)

	for key, value := range flat {
		if _, ok := quotaFieldToK8sName[key]; ok {
			limit[key] = value
		} else {
			extended[key] = value
		}
	}

	if len(extended) > 0 {
		limit[extendedLimitField] = extended
	}

	return limit
}

// validateQuotaConsistency checks the rules Rancher enforces between a project quota and its namespace default
// quota: both must be set together, set the same resources, and the default can't exceed the project quota.
func validateQuotaConsistency(projectQuota, namespaceDefault map[string]string) error {
	if len(projectQuota) == 0 && len(namespaceDefault) == 0 {
		return nil
	}

	if len(projectQuota) == 0 {
		return errors.New("a namespace default quota requires a project quota: also set projectQuota")
	}

	if len(namespaceDefault) == 0 {
		return errors.New("a project quota requires a namespace default quota for the same resources: also set namespaceDefaultQuota")
	}

	for _, key := range sortedKeys(projectQuota) {
		defaultValue, ok := namespaceDefault[key]
		if !ok {
			return fmt.Errorf("resource %q has a project quota but no namespace default quota: also set it in namespaceDefaultQuota", key)
		}

		limit, err := resource.ParseQuantity(projectQuota[key])
		if err != nil {
			return fmt.Errorf("invalid project quota for %q: %w", key, err)
		}

		def, err := resource.ParseQuantity(defaultValue)
		if err != nil {
			return fmt.Errorf("invalid namespace default quota for %q: %w", key, err)
		}

		if def.Cmp(limit) > 0 {
			return fmt.Errorf("namespace default quota for %q (%s) exceeds the project quota (%s)", key, defaultValue, projectQuota[key])
		}
	}

	for _, key := range sortedKeys(namespaceDefault) {
		if _, ok := projectQuota[key]; !ok {
			return fmt.Errorf("resource %q has a namespace default quota but no project quota: also set it in projectQuota", key)
		}
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
