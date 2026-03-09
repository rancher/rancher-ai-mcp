package fleet

import (
	"bytes"
	"context"
	"fmt"

	fleetv1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/troubleshooting"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type cli struct{}

func (c *cli) analiseFleetResources(ctx context.Context, restCfg *rest.Config) (string, error) {
	k8sClient, err := newFleetK8sClient(restCfg)
	if err != nil {
		return "", err
	}

	col := &troubleshooting.Collector{
		Namespace: "fleet-default", //TODO check
	}
	snapshot, err := col.CollectResources(ctx, k8sClient)
	if err != nil {
		zap.L().Error("failed to collect fleet resources", zap.Error(err))
		return "", fmt.Errorf("failed to collect fleet resources: %w", err)
	}
	var buf bytes.Buffer
	if err := troubleshooting.OutputIssues(&buf, []*troubleshooting.Snapshot{snapshot}); err != nil {
		return "", fmt.Errorf("failed to output fleet issues: %w", err)
	}

	return buf.String(), nil
}

// newFleetK8sClient builds a controller-runtime client with the k8s and Fleet schemes registered.
func newFleetK8sClient(restCfg *rest.Config) (k8sclient.Client, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to register k8s scheme: %w", err)
	}
	if err := fleetv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to register fleet scheme: %w", err)
	}

	k8sClient, err := k8sclient.New(restCfg, k8sclient.Options{Scheme: scheme})
	if err != nil {
		zap.L().Error("failed to create k8s client", zap.Error(err))
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	return k8sClient, nil
}
