package converter

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	MachineConfigGroup = "rke-machine-config.cattle.io"

	// CAPIKindPrefix is used to differentiate between resources which
	// share the same kind but are a part of different groups
	//(i.e. capicluster vs provisioningcluster)
	CAPIKindPrefix                    = "capi"
	CAPIGroup                         = "cluster.x-k8s.io"
	CAPIMachineResourceKind           = CAPIKindPrefix + "machine"
	CAPIClusterResourceKind           = CAPIKindPrefix + "cluster"
	CAPIMachineSetResourceKind        = CAPIKindPrefix + "machineset"
	CAPIMachineDeploymentResourceKind = CAPIKindPrefix + "machinedeployment"

	// ProvisioningKindPrefix is used to differentiate between resources which
	// share the same kind but are a part of different groups
	// (i.e. provisioningcluster vs managementcluster)
	ProvisioningKindPrefix          = "provisioning"
	ProvisioningGroup               = "provisioning.cattle.io"
	ProvisioningClusterResourceKind = ProvisioningKindPrefix + "cluster"

	// ManagementKindPrefix is used to differentiate between resources which
	// share the same kind but are a part of different groups
	// (i.e. managementcluster vs provisioningcluster)
	ManagementKindPrefix          = "management"
	ManagementGroup               = "management.cattle.io"
	ManagementClusterResourceKind = ManagementKindPrefix + "cluster"
)

// K8sKindsToGVRs maps lowercase Kubernetes resource kind names to their corresponding
// GroupVersionResource (GVR) identifiers. This mapping is used for dynamic client operations
// to resolve resource types across different API groups and versions.
var K8sKindsToGVRs = map[string]schema.GroupVersionResource{
	// --- CORE Kubernetes Resources (Group: "") ---
	"pod":                   {Group: "", Version: "v1", Resource: "pods"},
	"service":               {Group: "", Version: "v1", Resource: "services"},
	"configmap":             {Group: "", Version: "v1", Resource: "configmaps"},
	"secret":                {Group: "", Version: "v1", Resource: "secrets"},
	"event":                 {Group: "", Version: "v1", Resource: "events"},
	"namespace":             {Group: "", Version: "v1", Resource: "namespaces"},
	"node":                  {Group: "", Version: "v1", Resource: "nodes"},
	"serviceaccount":        {Group: "", Version: "v1", Resource: "serviceaccounts"},
	"persistentvolume":      {Group: "", Version: "v1", Resource: "persistentvolumes"},
	"persistentvolumeclaim": {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	"resourcequota":         {Group: "", Version: "v1", Resource: "resourcequotas"},
	"limitrange":            {Group: "", Version: "v1", Resource: "limitranges"},

	// --- Apps Resources (Group: "apps") ---
	"deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"statefulset": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"daemonset":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"replicaset":  {Group: "apps", Version: "v1", Resource: "replicasets"},

	// --- Batch Resources (Group: "batch") ---
	"job":     {Group: "batch", Version: "v1", Resource: "jobs"},
	"cronjob": {Group: "batch", Version: "v1", Resource: "cronjobs"},

	// --- Networking Resources (Group: "networking.k8s.io") ---
	"ingress":       {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	"networkpolicy": {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	"ingressclass":  {Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"},

	// --- Autoscaling Resources (Group: "autoscaling") ---
	"horizontalpodautoscaler": {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
	"vpa":                     {Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}, // Note: VPA is separate group

	// --- RBAC Resources (Group: "rbac.authorization.k8s.io") ---
	"role":               {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	"rolebinding":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"},
	"clusterrole":        {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
	"clusterrolebinding": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"},

	// --- Storage Resources (Group: "storage.k8s.io") ---
	"storageclass":     {Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"},
	"volumeattachment": {Group: "storage.k8s.io", Version: "v1", Resource: "volumeattachments"},

	// --- Node-Level Resources (Group: "node.k8s.io" / "storage.k8s.io") ---
	"runtimeclass": {Group: "node.k8s.io", Version: "v1", Resource: "runtimeclasses"},
	"csinode":      {Group: "storage.k8s.io", Version: "v1", Resource: "csinodes"},
	"csidriver":    {Group: "storage.k8s.io", Version: "v1", Resource: "csidrivers"},

	// --- Flow Control Resources (Group: "flowcontrol.apiserver.k8s.io") ---
	"flowschema":                 {Group: "flowcontrol.apiserver.k8s.io", Version: "v1", Resource: "flowschemas"},
	"prioritylevelconfiguration": {Group: "flowcontrol.apiserver.k8s.io", Version: "v1", Resource: "prioritylevelconfigurations"},

	// --- Admission Control Resources (Group: "admissionregistration.k8s.io") ---
	"validatingwebhookconfiguration":   {Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"},
	"mutatingwebhookconfiguration":     {Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"},
	"validatingadmissionpolicy":        {Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicies"},
	"validatingadmissionpolicybinding": {Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicybindings"},

	// --- API Extension Resources (Group: "apiextensions.k8s.io") ---
	"crd":                      {Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"},
	"customresourcedefinition": {Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"},

	// --- Discovery/Endpoint Resources (Group: "discovery.k8s.io") ---
	"endpointslice": {Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"},

	// --- Policy Resources (Group: "policy") ---
	"poddisruptionbudget": {Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},

	// --- Scheduling Resources (Group: "scheduling.k8s.io") ---
	"priorityclass": {Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses"},

	// --- METRICS Resources (Group: "metrics.k8s.io") ---
	"node.metrics.k8s.io": {Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"},
	"pod.metrics.k8s.io":  {Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"},

	// --- RANCHER CORE Resources (Group: "management.cattle.io") ---
	ManagementClusterResourceKind: {Group: ManagementGroup, Version: "v3", Resource: "clusters"},
	"project":                     {Group: ManagementGroup, Version: "v3", Resource: "projects"},
	"user":                        {Group: ManagementGroup, Version: "v3", Resource: "users"},
	"roletemplate":                {Group: ManagementGroup, Version: "v3", Resource: "roletemplates"},
	"globalrole":                  {Group: ManagementGroup, Version: "v3", Resource: "globalroles"},
	"globalrolebinding":           {Group: ManagementGroup, Version: "v3", Resource: "globalrolebindings"},
	"clusterroletemplatebinding":  {Group: ManagementGroup, Version: "v3", Resource: "clusterroletemplatebindings"},
	"projectroletemplatebinding":  {Group: ManagementGroup, Version: "v3", Resource: "projectroletemplatebindings"},
	"nodetemplate":                {Group: ManagementGroup, Version: "v3", Resource: "nodetemplates"},
	"nodedriver":                  {Group: ManagementGroup, Version: "v3", Resource: "nodedrivers"},

	// --- RANCHER PROVISIONING Resources (Group: "provisioning.cattle.io") ---
	ProvisioningClusterResourceKind: {Group: ProvisioningGroup, Version: "v1", Resource: "clusters"},

	// --- RANCHER VIRTUAL CLUSTER PROVISIONING Resources (Group: "k3k.io") ---
	"k3kcluster": {Group: "k3k.io", Version: "v1beta1", Resource: "clusters"},

	// --- RANCHER FLEET Resources (Group: "fleet.cattle.io") ---
	"bundle":           {Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundles"},
	"gitrepo":          {Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "gitrepos"},
	"bundledeployment": {Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "bundledeployments"},
	"clustergroup":     {Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "clustergroups"},
	"fleetcluster":     {Group: "fleet.cattle.io", Version: "v1alpha1", Resource: "clusters"}, // Renamed to avoid collision with management.cattle.io/v3/clusters

	// --- RANCHER CATTLE Resources (Group: "cattle.io") ---
	"setting": {Group: ManagementGroup, Version: "v3", Resource: "settings"},

	// --- CLUSTER API Resources (Group: "cluster.x-k8s.io") ---
	// NB: version is intentionally left empty as it can vary (v1beta1, v1beta2, etc.) depending on the version
	// of Rancher being used. Instead of hardcoding the version, we instead query all available versions when looking
	// up one of these resources.
	CAPIClusterResourceKind:           {Group: CAPIGroup, Version: "", Resource: "clusters"},
	CAPIMachineResourceKind:           {Group: CAPIGroup, Version: "", Resource: "machines"},
	CAPIMachineSetResourceKind:        {Group: CAPIGroup, Version: "", Resource: "machinesets"},
	CAPIMachineDeploymentResourceKind: {Group: CAPIGroup, Version: "", Resource: "machinedeployments"},
}
