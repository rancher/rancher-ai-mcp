package provisioning

import (
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/client/test"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestInvestigateFailedPlanApplicationPlan(t *testing.T) {
	fakeToken := "fakeToken"

	tests := map[string]struct {
		params         investigateFailedPlanApplicationParams
		fakeClientset  kubernetes.Interface
		fakeDynClient  *dynamicfake.FakeDynamicClient
		expectedResult string
		expectedError  string
	}{
		"plan investigation of a failed plan": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", "")),
			expectedResult: `[
				{
					"type": "create",
					"payload": {
						"apiVersion": "batch/v1",
						"kind": "Job",
						"metadata": {
							"name": "test-cluster-node-1-systemd-log-gatherer",
							"namespace": "cattle-system"
						},
						"spec": {
							"activeDeadlineSeconds": 10,
							"backoffLimit": 1,
							"completions": 1,
							"template": {
								"metadata": {},
								"spec": {
									"containers": [
										{
											"command": [
												"chroot",
												"/host",
												"bash",
												"-c",
												"if ! journalctl -eu rancher-system-agent.service --no-pager -o cat | grep PLAN_HASH -A 15 -B 10; then echo 'No logs found for the machine plan hash; showing the last 25 lines of service logs currently available'; journalctl -eu rancher-system-agent.service --no-pager -o cat | tail -n 25; fi; systemctl status rancher-system-agent.service -n 0; status=$?; if [ \"$status\" -eq 3 ]; then echo 'Service is inactive or failed'; exit 0; fi; exit \"$status\""
											],
											"image": "test-toolbox-image",
											"name": "journald-reader",
											"resources": {},
											"securityContext": {
												"allowPrivilegeEscalation": false,
												"capabilities": {
													"add": ["SYS_CHROOT"],
													"drop": ["ALL"]
												},
												"runAsUser": 0
											},
											"volumeMounts": [
												{
													"mountPath": "/host",
													"name": "host-root",
													"readOnly": true
												}
											]
										}
									],
									"nodeSelector": {
										"kubernetes.io/hostname": "test-cluster-node-1"
									},
									"restartPolicy": "Never",
									"volumes": [
										{
											"hostPath": {
												"path": "/",
												"type": "Directory"
											},
											"name": "host-root"
										}
									]
								}
							},
							"ttlSecondsAfterFinished": 120
						},
						"status": {}
					},
					"resource": {
						"name": "test-cluster-node-1-systemd-log-gatherer",
						"kind": "Job",
						"cluster": "test-cluster",
						"namespace": "cattle-system"
					}
				}
			]`,
		},
		"plan investigation on the local cluster": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "local",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "plans are not used to control the local cluster",
		},
		"plan investigation with empty node name": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: "",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "node name is required",
		},
		"plan investigation on a cluster without CAPI machines": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "did not find any CAPI machine resources for cluster test-cluster",
		},
		"plan investigation on an unknown node": {
			params: investigateFailedPlanApplicationParams{
				Cluster:  "test-cluster",
				NodeName: "unknown-node",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newCAPIMachine(testPlanNode, "fleet-default", "test-cluster", "Running", "")),
			expectedError: "failed to find node unknown-node",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := &client.Client{
				ClientSetCreator: func(inConfig *rest.Config) (kubernetes.Interface, error) {
					return tt.fakeClientset, nil
				},
				DynClientCreator: func(inConfig *rest.Config) (dynamic.Interface, error) {
					return tt.fakeDynClient, nil
				},
			}

			tools := NewTools(test.WrapClient(c, fakeToken), false)
			tools.toolboxImage = testToolboxImage
			req := &mcp.CallToolRequest{}
			req.Params = &mcp.CallToolParamsRaw{
				Name: "investigate-failed-plan-application-plan",
			}

			result, _, err := tools.investigateFailedPlanApplicationPlan(middleware.WithToken(t.Context(), fakeToken), req, tt.params)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
				return
			}

			require.NoError(t, err)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.Truef(t, ok, "expected type *mcp.TextContent")
			assert.JSONEq(t, tt.expectedResult, text.Text)

			// the plan is a dry run, so no job should have been created
			_, err = tt.fakeDynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace).
				Get(t.Context(), fmt.Sprintf("%s-systemd-log-gatherer", tt.params.NodeName), metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "expected no job to be created, got: %v", err)
		})
	}
}
