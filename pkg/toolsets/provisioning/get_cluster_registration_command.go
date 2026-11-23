package provisioning

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rancher/rancher-ai-mcp/internal/middleware"
	"github.com/rancher/rancher-ai-mcp/pkg/client"
	"github.com/rancher/rancher-ai-mcp/pkg/converter"
	"github.com/rancher/rancher-ai-mcp/pkg/response"
	"github.com/rancher/rancher-ai-mcp/pkg/utils"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// clusterRegistrationTokenGVR is deliberately not registered in converter.K8sKindsToGVRs.
// That map is shared by the generic Kubernetes resource tools (list, get, create, patch),
// so adding the kind there would let any generic tool call read, or possibly modify,
// registration tokens. Those tokens are join credentials: anyone holding one can register
// a node with the cluster. Keeping the GVR private to this tool means access goes through
// getClusterRegistrationCommand, which only reads them and documents that the output is sensitive.
var clusterRegistrationTokenGVR = schema.GroupVersionResource{Group: converter.ManagementGroup, Version: "v3", Resource: "clusterregistrationtokens"}

// secretGVR is private to this tool for the same reason as clusterRegistrationTokenGVR: the generic
// resource tools should not be given a new way to read the secret holding the registration token.
var secretGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

type getClusterRegistrationCommandParams struct {
	Cluster   string `json:"cluster" jsonschema:"the name of the Kubernetes cluster"`
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace of the provisioning cluster. Defaults to fleet-default"`
}

// registrationCommands contains the registration details of a cluster.
type registrationCommands struct {
	Name                       string `json:"name"`
	Token                      string `json:"token,omitempty"`
	NodeCommand                string `json:"nodeCommand,omitempty"`
	InsecureNodeCommand        string `json:"insecureNodeCommand,omitempty"`
	WindowsNodeCommand         string `json:"windowsNodeCommand,omitempty"`
	InsecureWindowsNodeCommand string `json:"insecureWindowsNodeCommand,omitempty"`
	TokenSecretName            string `json:"tokenSecretName,omitempty"`
	ExpiresAt                  string `json:"expiresAt,omitempty"`
	ManifestURL                string `json:"manifestUrl,omitempty"`
	Command                    string `json:"command,omitempty"`
	InsecureCommand            string `json:"insecureCommand,omitempty"`
}

// isEmpty returns true if the token has nothing to report: no token, no secret to read it from,
// and none of the commands or URLs. Name and ExpiresAt are not enough on their own.
func (rc *registrationCommands) isEmpty() bool {
	return rc.Token == "" && rc.TokenSecretName == "" &&
		rc.NodeCommand == "" && rc.InsecureNodeCommand == "" &&
		rc.WindowsNodeCommand == "" && rc.InsecureWindowsNodeCommand == "" &&
		rc.ManifestURL == "" && rc.Command == "" && rc.InsecureCommand == ""
}

func (params *getClusterRegistrationCommandParams) validate(toolReq *mcp.CallToolRequest) (*zap.Logger, error) {
	params.Namespace = clusterResourcesNamespace(params.Cluster, params.Namespace)

	log := utils.NewChildLogger(toolReq, map[string]string{
		"cluster":   params.Cluster,
		"namespace": params.Namespace,
	})

	// The cluster may be given as a display name, which can contain characters that
	// aren't valid in a resource name, so only require a value here. The resource
	// name rules are applied to the direct lookup in getClusterRegistrationCommand.
	if params.Cluster == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if params.Cluster == LocalCluster {
		return nil, fmt.Errorf("the local cluster does not support registering machines")
	}

	return log, nil
}

// getClusterRegistrationCommand returns the registration commands and URLs of a cluster.
func (t *Tools) getClusterRegistrationCommand(ctx context.Context, toolReq *mcp.CallToolRequest, params getClusterRegistrationCommandParams) (*mcp.CallToolResult, any, error) {
	log, err := params.validate(toolReq)
	if err != nil {
		return nil, nil, err
	}

	log.Debug("Getting cluster registration command")

	token := middleware.Token(ctx)
	// A name that isn't a valid resource name can only be a display name, so skip the direct lookup.
	var provCluster *unstructured.Unstructured

	invalidName := validateClusterName(params.Cluster) != nil
	if !invalidName {
		provCluster, err = t.client.GetResource(ctx, client.GetParams{
			Cluster:   LocalCluster,
			Kind:      converter.ProvisioningClusterResourceKind,
			Namespace: params.Namespace,
			Name:      params.Cluster,
			Token:     token,
		})
	}

	if invalidName || errors.IsNotFound(err) {
		// imported and hosted clusters are named after their management
		// cluster ID, so also try to match the display name.
		provCluster, err = t.findProvisioningClusterByDisplayName(ctx, params.Namespace, params.Cluster, token)
	}
	if err != nil {
		log.Error("failed to get provisioning cluster", zap.Error(err))
		return nil, nil, err
	}

	if pools, _, _ := unstructured.NestedSlice(provCluster.Object, "spec", "rkeConfig", "machinePools"); len(pools) > 0 {
		return nil, nil, fmt.Errorf("cluster %q is provisioned with machine pools and does not use registration commands, use scaleClusterNodePool to add machines", params.Cluster)
	}

	mgmtName, _, _ := unstructured.NestedString(provCluster.Object, "status", "clusterName")
	if mgmtName == "" {
		return nil, nil, fmt.Errorf("cluster %q has no management cluster yet, try again once it has been created", params.Cluster)
	}

	clusterType := t.registrationClusterType(ctx, provCluster, mgmtName, token)

	// The GVR is passed explicitly instead of looking the kind up by name:
	// see clusterRegistrationTokenGVR.
	resourceInterface, err := t.client.GetResourceInterface(ctx, token, mgmtName, LocalCluster, clusterRegistrationTokenGVR)
	if err != nil {
		log.Error("failed to get cluster registration token client", zap.Error(err))
		return nil, nil, err
	}
	tokenList, err := resourceInterface.List(ctx, metav1.ListOptions{})
	if err != nil && !errors.IsNotFound(err) {
		log.Error("failed to list cluster registration tokens", zap.Error(err))
		return nil, nil, err
	}
	var tokens []unstructured.Unstructured
	if tokenList != nil {
		tokens = tokenList.Items
	}

	var results []registrationCommands
	for i := range tokens {
		obj := &tokens[i]
		status, _ := obj.Object["status"].(map[string]any)
		str := func(k string) string { s, _ := status[k].(string); return s }
		rc := registrationCommands{
			Name:                       obj.GetName(),
			Token:                      str("token"),
			NodeCommand:                str("nodeCommand"),
			InsecureNodeCommand:        str("insecureNodeCommand"),
			WindowsNodeCommand:         str("windowsNodeCommand"),
			InsecureWindowsNodeCommand: str("insecureWindowsNodeCommand"),
			TokenSecretName:            str("tokenSecretName"),
			ExpiresAt:                  str("expiresAt"),
			ManifestURL:                str("manifestUrl"),
			Command:                    str("command"),
			InsecureCommand:            str("insecureCommand"),
		}
		// newer Rancher versions keep the token in a secret and leave
		// status.token empty, with a {token} placeholder in the commands.
		// The commands can also be blank when the caller can't read the secret, so
		// entries with a tokenSecretName are kept and the secret read reports the error.
		if rc.isEmpty() {
			continue
		}
		if rc.Token == "" && rc.TokenSecretName != "" {
			if err := t.interpolateToken(ctx, log, &rc, mgmtName, token); err != nil {
				return nil, nil, err
			}
		}
		results = append(results, rc)
	}

	var data any
	if len(results) == 0 {
		data = "no registration token found for this cluster"
	} else {
		data = map[string]any{
			"cluster":     provCluster.GetName(),
			"clusterType": clusterType,
			"note":        registrationNote(clusterType),
			"tokens":      results,
		}
	}

	mcpResponse, err := response.CreateMcpResponseAny(data)
	if err != nil {
		log.Error("failed to create mcp response", zap.Error(err))
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: mcpResponse}},
	}, nil, nil
}

// interpolateToken reads the registration token from the secret named in the ClusterRegistrationToken
// status, using the caller's credentials, and replaces the {token} placeholder in the commands with it.
// It returns an error if the secret can't be read, so that a caller without access to the secret is
// told so instead of being given commands that don't work. The secret lives in the same namespace as
// the ClusterRegistrationToken (the management cluster namespace).
func (t *Tools) interpolateToken(ctx context.Context, log *zap.Logger, rc *registrationCommands, namespace, authToken string) error {
	secrets, err := t.client.GetResourceInterface(ctx, authToken, namespace, LocalCluster, secretGVR)
	if err != nil {
		return err
	}

	secret, err := secrets.Get(ctx, rc.TokenSecretName, metav1.GetOptions{})
	if err != nil {
		log.Error("failed to read registration token secret", zap.String("secret", rc.TokenSecretName), zap.Error(err))

		switch {
		case errors.IsForbidden(err), errors.IsUnauthorized(err):
			return fmt.Errorf("you do not have permission to read the registration token secret %q in namespace %q, so the registration command can't be completed: %w", rc.TokenSecretName, namespace, err)
		case errors.IsNotFound(err):
			return fmt.Errorf("the registration token secret %q was not found in namespace %q: %w", rc.TokenSecretName, namespace, err)
		}

		return fmt.Errorf("failed to read the registration token secret %q in namespace %q: %w", rc.TokenSecretName, namespace, err)
	}

	encoded, _, _ := unstructured.NestedString(secret.Object, "data", "token")

	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(value) == 0 {
		return fmt.Errorf("the registration token secret %q in namespace %q has no usable token", rc.TokenSecretName, namespace)
	}

	for _, field := range []*string{&rc.NodeCommand, &rc.InsecureNodeCommand, &rc.WindowsNodeCommand, &rc.InsecureWindowsNodeCommand, &rc.ManifestURL, &rc.Command, &rc.InsecureCommand} {
		*field = strings.ReplaceAll(*field, "{token}", string(value))
	}

	return nil
}

// registrationClusterType returns "custom" for clusters with an rkeConfig, "hosted" for EKS, AKS and GKE
// clusters, and "imported" otherwise. The management cluster is only read to tell hosted clusters from
// imported ones; if it can't be read the cluster is reported as imported.
func (t *Tools) registrationClusterType(ctx context.Context, provCluster *unstructured.Unstructured, mgmtName, token string) string {
	if _, found, _ := unstructured.NestedMap(provCluster.Object, "spec", "rkeConfig"); found {
		return "custom"
	}

	mgmtCluster, err := t.client.GetResource(ctx, client.GetParams{
		Cluster: LocalCluster,
		Kind:    converter.ManagementClusterResourceKind,
		Name:    mgmtName,
		Token:   token,
	})
	if err != nil {
		return "imported"
	}
	for _, key := range []string{"eksConfig", "aksConfig", "gkeConfig"} {
		if cfg, found, _ := unstructured.NestedMap(mgmtCluster.Object, "spec", key); found && len(cfg) > 0 {
			return "hosted"
		}
	}

	return "imported"
}

func registrationNote(clusterType string) string {
	if clusterType == "custom" {
		return "Run nodeCommand on the new machine, adding the role flags (--etcd, --controlplane, --worker) for the node. Prefer the secure commands: the insecure variants skip TLS certificate verification, so only use them when the Rancher certificate is not trusted but the network is otherwise trusted."
	}
	if clusterType == "hosted" {
		return "This is a hosted cluster (EKS, AKS or GKE): its nodes are managed by the cloud provider, not added through Rancher. Use command (or insecureCommand) only to register the cluster's agent. Prefer the secure commands: the insecure variants skip TLS certificate verification, so only use them when the Rancher certificate is not trusted but the network is otherwise trusted."
	}
	return "This is an imported cluster: machines are not added through Rancher. Use command (or insecureCommand) only to register the cluster's agent; add nodes with the cluster's own tooling. Prefer the secure commands: the insecure variants skip TLS certificate verification, so only use them when the Rancher certificate is not trusted but the network is otherwise trusted."
}

// findProvisioningClusterByDisplayName finds a provisioning cluster by the display name of its management cluster.
// The provisioning cluster annotation is only a hint: a match is only accepted if the management cluster's
// spec.displayName confirms it, and it is an error if more than one cluster matches.
func (t *Tools) findProvisioningClusterByDisplayName(ctx context.Context, namespace, displayName, token string) (*unstructured.Unstructured, error) {
	clusters, err := t.client.GetResources(ctx, client.ListParams{
		Cluster:   LocalCluster,
		Kind:      converter.ProvisioningClusterResourceKind,
		Namespace: namespace,
		Token:     token,
	})
	if err != nil {
		return nil, err
	}

	var matches []*unstructured.Unstructured
	for _, c := range clusters {
		if c.GetAnnotations()["provisioning.cattle.io/management-cluster-display-name"] != displayName {
			continue
		}
		mgmtName, _, _ := unstructured.NestedString(c.Object, "status", "clusterName")
		if mgmtName == "" {
			continue
		}
		mgmtCluster, err := t.client.GetResource(ctx, client.GetParams{
			Cluster: LocalCluster,
			Kind:    converter.ManagementClusterResourceKind,
			Name:    mgmtName,
			Token:   token,
		})
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if name, _, _ := unstructured.NestedString(mgmtCluster.Object, "spec", "displayName"); name == displayName {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("cluster %q not found in namespace %q (checked cluster names and display names), use listClusters to see the available clusters", displayName, namespace)
	case 1:
		return matches[0], nil
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m.GetName())
	}
	return nil, fmt.Errorf("display name %q matches several clusters in namespace %q (%s), use the cluster name instead", displayName, namespace, strings.Join(names, ", "))
}
