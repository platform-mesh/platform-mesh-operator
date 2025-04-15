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
		// Apply webhooks at the very last as the  certificates are not yet ready blocking the earlier account creation
		{
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
var AccountOperatorMutatingWebhookName = "openmfp-account-operator-mutating-webhook-configuration"
var AccountOperatorWorkspace = "root:openmfp-system"
var DefaultProviderConnection = corev1alpha1.ProviderConnection{
	EndpointSliceName: "core.openmfp.org",
	Path:              "root:openmfp-system",
	Secret:            "openmfp-operator-kubeconfig",
}
