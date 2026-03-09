package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/rancher/fleet/pkg/troubleshooting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeCollector is a test double for resourceCollector.
type fakeCollector struct {
	snapshot *troubleshooting.Snapshot
	err      error
}

func (f *fakeCollector) CollectResources(_ context.Context, _ k8sclient.Client) (*troubleshooting.Snapshot, error) {
	return f.snapshot, f.err
}

// fakeK8sClient returns a nil k8sclient.Client and optionally an error.
// The real client is never used by the fake collector, so nil is safe.
func fakeK8sClientFactory(err error) k8sClientFactory {
	return func(_ *rest.Config) (k8sclient.Client, error) {
		return nil, err
	}
}

func TestAnaliseFleetResources(t *testing.T) {
	dummyRestCfg := &rest.Config{}

	tests := map[string]struct {
		collector    resourceCollector
		outputIssues issueOutputter
		k8sFactory   k8sClientFactory
		expectedOut  string
		expectedErr  string
	}{
		"returns report on success": {
			collector: &fakeCollector{
				snapshot: &troubleshooting.Snapshot{Timestamp: "2026-01-01T00:00:00Z"},
			},
			outputIssues: func(w io.Writer, _ []*troubleshooting.Snapshot) error {
				_, err := fmt.Fprint(w, "fleet is healthy")
				return err
			},
			k8sFactory:  fakeK8sClientFactory(nil),
			expectedOut: "fleet is healthy",
		},
		"returns empty string when outputIssues writes nothing": {
			collector: &fakeCollector{
				snapshot: &troubleshooting.Snapshot{},
			},
			outputIssues: func(_ io.Writer, _ []*troubleshooting.Snapshot) error {
				return nil
			},
			k8sFactory:  fakeK8sClientFactory(nil),
			expectedOut: "",
		},
		"error creating k8s client is propagated": {
			collector:    &fakeCollector{},
			outputIssues: troubleshooting.OutputIssues,
			k8sFactory:   fakeK8sClientFactory(errors.New("cannot reach cluster")),
			expectedErr:  "cannot reach cluster",
		},
		"error from CollectResources is wrapped": {
			collector: &fakeCollector{
				err: errors.New("api server unavailable"),
			},
			outputIssues: troubleshooting.OutputIssues,
			k8sFactory:   fakeK8sClientFactory(nil),
			expectedErr:  "failed to collect fleet resources: api server unavailable",
		},
		"error from OutputIssues is wrapped": {
			collector: &fakeCollector{
				snapshot: &troubleshooting.Snapshot{},
			},
			outputIssues: func(_ io.Writer, _ []*troubleshooting.Snapshot) error {
				return errors.New("write error")
			},
			k8sFactory:  fakeK8sClientFactory(nil),
			expectedErr: "failed to output fleet issues: write error",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := &cli{
				collector:    tc.collector,
				outputIssues: tc.outputIssues,
				newK8sClient: tc.k8sFactory,
			}

			out, err := c.analiseFleetResources(context.Background(), dummyRestCfg)

			if tc.expectedErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tc.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out)
		})
	}
}
