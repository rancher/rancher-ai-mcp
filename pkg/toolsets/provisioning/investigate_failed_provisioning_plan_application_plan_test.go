package provisioning

import (
	"encoding/json"
	"fmt"
	"strings"
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
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster"),
				newFullMachinePlanSecret(testPlanNode)),
			expectedResult: `[
				{
					"type": "create",
					"payload": {
						"apiVersion": "batch/v1",
						"kind": "Job",
						"metadata": {
							"generateName": "test-cluster-node-1-systemd-log-gatherer-",
							"namespace": "cattle-system"
						},
						"spec": {
							"activeDeadlineSeconds": 180,
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
												__SCRIPT__
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
									"nodeName": "test-node-1",
									"restartPolicy": "Never",
									"tolerations": [{"operator": "Exists"}],
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
						"name": "test-cluster-node-1-systemd-log-gatherer-<generated>",
						"kind": "Job",
						"cluster": "test-cluster",
						"namespace": "cattle-system"
					}
				}
			]`,
		},
		"plan investigation on the local cluster": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "local",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "plans are not used to control the local cluster",
		},
		"plan investigation with empty node name": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: "",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(),
			expectedError: "machine name is required",
		},
		"plan investigation on a cluster without CAPI machines": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: testPlanNode,
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newManagementCluster("test-cluster", true)),
			expectedError: `no CAPI machine resources and its machine provider is ""`,
		},
		"plan investigation on an unknown node": {
			params: investigateFailedPlanApplicationParams{
				Cluster:     "test-cluster",
				MachineName: "unknown-node",
			},
			fakeClientset: newFakeClientSet(),
			fakeDynClient: newPlanApplicationDynClient(
				newPlanMachine(testPlanNode, "test-cluster")),
			expectedError: "failed to find machine unknown-node",
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
			expectedScript, err := json.Marshal(expectedLogScript)
			require.NoError(t, err)
			assert.JSONEq(t, strings.Replace(tt.expectedResult, "__SCRIPT__", string(expectedScript), 1), text.Text)

			// the plan is a dry run, so no job should have been created
			_, err = tt.fakeDynClient.Resource(converter.K8sKindsToGVRs["job"]).Namespace(DefaultRancherResourcesNamespace).
				Get(t.Context(), fmt.Sprintf("%s-systemd-log-gatherer", tt.params.MachineName), metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "expected no job to be created, got: %v", err)
		})
	}
}

// expectedLogScript is the script of the log gatherer job in the preview, where the plan hash is not known yet.
var expectedLogScript = "hits=$(journalctl -u rancher-system-agent.service --no-pager -o cat | grep PLAN_HASH -A 15 -B 10 | tail -n 65); if [ -n \"$hits\" ]; then printf '%s\\n' \"$hits\"; echo '--- system-agent execution lines for the machine plan hash ---'; journalctl -u rancher-system-agent.service --no-pager -o cat | awk '/PLAN_HASH/ && !seen {seen=1} /Applying one-time instructions for plan with checksum PLAN_HASH/ {split(\"\", l); n=0; seen=1} seen && n < 5000 {l[++n]=$0} END {for (i=1; i<=n; i++) print l[i]}' | grep -F -e '[Applyinator]' -e 'error executing instruction' -e '[PLAN_HASH_' | awk -v head=37 -v tail=37 'stopped { next }\n{ total++ }\ntotal <= head { print; if (index($0, \"error executing instruction\")) stopped = 1; next }\n{ ring[cnt++ % tail] = $0; if (index($0, \"error executing instruction\")) stopped = 1 }\nEND {\n  omitted = (cnt > tail) ? cnt - tail : 0\n  if (omitted > 0) print \"... \" omitted \" execution lines omitted ...\"\n  start = (omitted > 0) ? cnt % tail : 0\n  n = (omitted > 0) ? tail : cnt\n  for (i = 0; i < n; i++) print ring[(start + i) % tail]\n}'; else echo 'No logs found for the machine plan hash; showing the last 25 lines of service logs currently available'; journalctl -u rancher-system-agent.service --no-pager -o cat | tail -n 25; fi; systemctl status rancher-system-agent.service -n 0; status=$?; if [ \"$status\" -ne 0 ]; then echo \"systemctl status returned exit code $status\"; fi; exit 0"
