package subroutines

import corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"

type DirectoryStructure struct {
	Workspaces []WorkspaceDirectory `yaml:"workspaces"`
}

type WorkspaceDirectory struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

var DirManifestStructure = DirectoryStructure{
	Workspaces: []WorkspaceDirectory{
		{
			Name: "root",
			Files: []string{
				"/operator/setup/workspace-openmfp-system.yaml",
				"/operator/setup/workspace-type-provider.yaml",
				"/operator/setup/workspace-type-providers.yaml",
				"/operator/setup/workspace-type-fga.yaml",
				"/operator/setup/workspace-type-org.yaml",
				"/operator/setup/workspace-type-orgs.yaml",
				"/operator/setup/workspace-type-account.yaml",
				"/operator/setup/workspace-orgs.yaml",
			},
		},
		{
			Name: "root:openmfp-system",
			Files: []string{
				"/operator/setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexport-fga.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexport-kcp.io.yaml",
				"/operator/setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexportendpointslice-fga.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiexportendpointslice-kcp.io.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-accountinfos.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-accounts.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-authorizationmodels.core.openmfp.org.yaml",
				"/operator/setup/01-openmfp-system/apiresourceschema-stores.core.openmfp.org.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"/operator/setup/02-orgs/account-root-org.yaml",
				"/operator/setup/02-orgs/workspace-root-org.yaml",
			},
		},
		{ // applied last because there is not running webhook during the tests
			Name: "root:openmfp-system",
			Files: []string{
				"/operator/setup/01-openmfp-system/mutatingwebhookconfiguration-admissionregistration.k8s.io.yaml",
			},
		},
	},
}

var AccountOperatorMutatingWebhookSecretName = "openmfp-account-operator-webhook-server-cert"
var AccountOperatorMutatingWebhookSecretNamespace = "openmfp-system"
var DefaultCASecretKey = "ca.crt"
var AccountOperatorMutatingWebhookName = "account-operator.webhooks.core.openmfp.org"
var AccountOperatorWorkspace = "root:openmfp-system"
var DefaultProviderConnections = []corev1alpha1.ProviderConnection{
	{
		EndpointSliceName: "core.openmfp.org",
		Path:              "root:openmfp-system",
		Secret:            "account-operator-kubeconfig",
	},
	{
		EndpointSliceName: "fga.openmfp.org",
		Path:              "root:openmfp-system",
		Secret:            "fga-operator-kubeconfig",
	},
	{
		EndpointSliceName: "kcp.io",
		Path:              "root:openmfp-system",
		Secret:            "kubernetes-grapqhl-gateway-kubeconfig",
	},
}
var DEFAULT_WEBHOOK_CONFIGURATION = corev1alpha1.WebhookConfiguration{
	SecretRef: corev1alpha1.SecretReference{
		Name:      AccountOperatorMutatingWebhookSecretName,
		Namespace: AccountOperatorMutatingWebhookSecretNamespace,
	},
	SecretData: DefaultCASecretKey,
	WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
		ApiVersion: "admissionregistration.k8s.io/v1",
		Kind:       "MutatingWebhookConfiguration",
		Name:       AccountOperatorMutatingWebhookName,
		Path:       AccountOperatorWorkspace,
	},
}
