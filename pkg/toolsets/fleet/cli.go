package fleet

import (
	"bytes"
	"context"
	"fmt"
	"io"

	fleetv1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/troubleshooting"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// resourceCollector abstracts troubleshooting.Collector to allow testing without a real cluster.
type resourceCollector interface {
	CollectResources(ctx context.Context, c k8sclient.Client) (*troubleshooting.Snapshot, error)
}

// issueOutputter is a function that writes Fleet issues from one or more snapshots to a writer.
type issueOutputter func(w io.Writer, snapshots []*troubleshooting.Snapshot) error

// k8sClientFactory builds a controller-runtime client from a REST config.
type k8sClientFactory func(restCfg *rest.Config) (k8sclient.Client, error)

// collectorFactory creates a resourceCollector scoped to the given namespace.
type collectorFactory func(namespace string) resourceCollector

type cli struct {
	newCollector collectorFactory
	outputIssues issueOutputter
	newK8sClient k8sClientFactory
}

// newCLI returns a cli wired to the real Fleet troubleshooting implementation.
func newCLI() *cli {
	return &cli{
		newCollector: func(namespace string) resourceCollector {
			return &troubleshooting.Collector{Namespace: namespace}
		},
		outputIssues: troubleshooting.OutputIssues,
		newK8sClient: newFleetK8sClient,
	}
}

func (c *cli) analiseFleetResources(ctx context.Context, restCfg *rest.Config, namespace string) (string, error) {
	k8sClient, err := c.newK8sClient(restCfg)
	if err != nil {
		return "", err
	}

	snapshot, err := c.newCollector(namespace).CollectResources(ctx, k8sClient)
	if err != nil {
		zap.L().Error("failed to collect fleet resources", zap.Error(err))
		return "", fmt.Errorf("failed to collect fleet resources: %w", err)
	}

	var buf bytes.Buffer
	if err := c.outputIssues(&buf, []*troubleshooting.Snapshot{snapshot}); err != nil {
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
