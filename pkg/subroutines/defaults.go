package subroutines

import corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"

type DirectoryStructure struct {
	Workspaces []WorkspaceDirectory `yaml:"workspaces"`
}

type WorkspaceDirectory struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

// apply manifests for folder dir to KCP
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
				"/operator/setup/01-openmfp-system/mutatingwebhookconfiguration-admissionregistration.k8s.io.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"/operator/setup/02-orgs/account-root-org.yaml",
				"/operator/setup/02-orgs/workspace-root-org.yaml",
			},
		},
	},
}

var WEBHOOK_DEFAULT_K8S_SECRET_NAME = "openmfp-iam-authorization-webhook-cert"
var WEBHOOK_DEFAULT_K8S_SECRET_NAMESPACE = "default"
var WEBHOOK_DEFAULT_K8S_SECRET_DATA = "ca.crt"
var WEBHOOK_DEFAULT_KCP_WEBHOOK_NAME = "openmfp-account-operator-mutating-webhook-configuration"
var WEBHOOK_DEFAULT_KCP_PATH = "root:openmfp-system"
var DEFAULT_PROVIDER_CONNECTION = corev1alpha1.ProviderConnection{
	EndpointSliceName: "core.openmfp.org",
	Path:              "root:openmfp-system",
	Secret:            "openmfp-operator-kubeconfig",
}
