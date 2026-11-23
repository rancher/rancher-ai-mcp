package utils

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// features and settings are global, so they are always read from the local cluster.
	localCluster = "local"

	featureKind = "feature"
	settingKind = "setting"
)

// ResourceGetter is the part of a Rancher client needed to read single resources. The clients used by tool sets
// implement it.
type ResourceGetter interface {
	GetResource(ctx context.Context, params client.GetParams) (*unstructured.Unstructured, error)
}

// IsFeatureEnabled reports whether a Rancher feature is enabled. A feature which does not exist is not supported by this
// version of Rancher, and is reported as disabled.
func IsFeatureEnabled(ctx context.Context, c ResourceGetter, name string) (bool, error) {
	feature, err := c.GetResource(ctx, client.GetParams{
		Cluster: localCluster,
		Kind:    featureKind,
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get feature %s: %w", name, err)
	}

	// the same order Rancher uses to compute a feature's value: a locked value, the value set on the feature, then its default.
	for _, fields := range [][]string{{"status", "lockedValue"}, {"spec", "value"}, {"status", "default"}} {
		value, set, err := optionalBool(feature.Object, fields...)
		if err != nil {
			return false, fmt.Errorf("failed to read %s of feature %s: %w", strings.Join(fields, "."), name, err)
		}

		if set {
			return value, nil
		}
	}

	return false, nil
}

// GetSettingValue returns the effective value of a Rancher setting, which is its value, or its default if the value is
// empty. found is false if the setting does not exist.
func GetSettingValue(ctx context.Context, c ResourceGetter, name string) (value string, found bool, err error) {
	setting, err := c.GetResource(ctx, client.GetParams{
		Cluster: localCluster,
		Kind:    settingKind,
		Name:    name,
		Token:   middleware.Token(ctx),
	})
	if apierrors.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to get setting %s: %w", name, err)
	}

	// settings are not spec'd objects, the value and default are top level fields.
	value, _, err = unstructured.NestedString(setting.Object, "value")
	if err != nil {
		return "", false, fmt.Errorf("failed to read the value of setting %s: %w", name, err)
	}

	if strings.TrimSpace(value) != "" {
		return value, true, nil
	}

	value, _, err = unstructured.NestedString(setting.Object, "default")
	if err != nil {
		return "", false, fmt.Errorf("failed to read the default of setting %s: %w", name, err)
	}

	return value, true, nil
}

// IsTrue reports whether a setting or annotation value is "true", ignoring case and surrounding whitespace.
func IsTrue(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

// optionalBool reads a boolean field which may be absent or null. set is false in both of those cases.
func optionalBool(obj map[string]interface{}, fields ...string) (value, set bool, err error) {
	raw, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if err != nil {
		return false, false, err
	}

	if !found || raw == nil {
		return false, false, nil
	}

	value, ok := raw.(bool)
	if !ok {
		return false, false, fmt.Errorf("expected a boolean, got %T", raw)
	}

	return value, true, nil
}
