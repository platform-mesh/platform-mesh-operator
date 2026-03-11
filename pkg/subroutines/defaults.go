package subroutines

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

var AccountOperatorWebhookSecretName = "account-operator-webhook-server-cert"
var AccountOperatorWebhookSecretNamespace = "platform-mesh-system"

var DefaultCASecretKey = "ca.crt"
var AccountOperatorMutatingWebhookName = "account-operator.webhooks.core.platform-mesh.io"
var AccountOperatorValidatingWebhookName = "organization-validator.webhooks.core.platform-mesh.io"

var SecurityOperatorWebhookCASecretName = "security-operator-ca-secret"
var IdentityProviderValidatingWebhookName = "identityproviderconfiguration-validator.webhooks.core.platform-mesh.io"
var AccountOperatorWorkspace = "root:platform-mesh-system"
var DefaultProviderConnections = []corev1alpha1.ProviderConnection{
	// account-operator: scoped supported (APIExport).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "account-operator-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
	},
	// rebac: scoped; needs root:orgs access (RootOrgAccess) + get LogicalCluster + Stores. ensureRootOrgAccess is best-effort so secret is created first.
	{
		Path:           "root:platform-mesh-system",
		Secret:         "rebac-authz-webhook-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
		ServiceAccountPermissions: &corev1alpha1.ServiceAccountPermissions{
			RootOrgAccess:           ptr.To(true),
			EnableGetLogicalCluster: ptr.To(true),
			EnableStoresAccess:      ptr.To(true),
		},
	},
	// security-operator: scoped supported (APIExport).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "security-operator-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
	},
	// kubernetes-graphql-gateway: needs get logicalclusters in root:orgs; admin SA works when root:orgs RBAC uses provider workspace cluster ID (see kubeconfig_scoped.go).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "kubernetes-grapqhl-gateway-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthAdminCertificate,
	},
	// extension-manager: scoped supported (APIExport).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "extension-manager-operator-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
	},
	// iam-service: scoped supported (APIExport); needs get logicalcluster (e.g. for workspace resolution).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "iam-service-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
		ServiceAccountPermissions: &corev1alpha1.ServiceAccountPermissions{
			EnableGetLogicalCluster: ptr.To(true),
		},
	},
	// portal: virtual workspace path, admin cert in defaults.
	{
		RawPath:        ptr.To("/services/contentconfigurations"),
		Secret:         "portal-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthAdminCertificate,
	},
	// security-initializer/terminator: root path, admin cert (no scoped export for root).
	{
		Path:           "root",
		Secret:         "security-initializer-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthAdminCertificate,
	},
	{
		Path:           "root",
		Secret:         "security-terminator-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthAdminCertificate,
	},
	// init-agent: scoped supported (APIExport); needs initialization.kcp.io inittargets (list/watch).
	{
		Path:           "root:platform-mesh-system",
		Secret:         "init-agent-kubeconfig",
		KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
		APIExportName:  "core.platform-mesh.io",
		ServiceAccountPermissions: &corev1alpha1.ServiceAccountPermissions{
			EnableInitTargetsAccess: ptr.To(true),
		},
	},
}
var DEFAULT_WEBHOOK_CONFIGURATION = corev1alpha1.WebhookConfiguration{
	SecretRef: corev1alpha1.SecretReference{
		Name:      AccountOperatorWebhookSecretName,
		Namespace: AccountOperatorWebhookSecretNamespace,
	},
	SecretData: DefaultCASecretKey,
	WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
		ApiVersion: "admissionregistration.k8s.io/v1",
		Kind:       "MutatingWebhookConfiguration",
		Name:       AccountOperatorMutatingWebhookName,
		Path:       AccountOperatorWorkspace,
	},
}

var DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION = corev1alpha1.WebhookConfiguration{
	SecretRef: corev1alpha1.SecretReference{
		Name:      AccountOperatorWebhookSecretName,
		Namespace: AccountOperatorWebhookSecretNamespace,
	},
	SecretData: DefaultCASecretKey,
	WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
		ApiVersion: "admissionregistration.k8s.io/v1",
		Kind:       "ValidatingWebhookConfiguration",
		Name:       AccountOperatorValidatingWebhookName,
		Path:       AccountOperatorWorkspace,
	},
}

var DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION = corev1alpha1.WebhookConfiguration{
	SecretRef: corev1alpha1.SecretReference{
		Name:      SecurityOperatorWebhookCASecretName,
		Namespace: AccountOperatorWebhookSecretNamespace,
	},
	SecretData: DefaultCASecretKey,
	WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
		ApiVersion: "admissionregistration.k8s.io/v1",
		Kind:       "ValidatingWebhookConfiguration",
		Name:       IdentityProviderValidatingWebhookName,
		Path:       AccountOperatorWorkspace,
	},
}

var DEFAULT_WAIT_CONFIG = corev1alpha1.WaitConfig{
	ResourceTypes: []corev1alpha1.ResourceType{
		{
			APIVersions: v1.APIVersions{
				Versions: []string{"v2"},
			},
			GroupKind: v1.GroupKind{
				Group: "helm.toolkit.fluxcd.io",
				Kind:  "HelmRelease",
			},
			Namespace: "default",
			LabelSelector: v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key:      "helm.toolkit.fluxcd.io/name",
						Operator: v1.LabelSelectorOpIn,
						Values:   []string{"platform-mesh-operator-components"},
					},
				},
			},
			ConditionStatus:  v1.ConditionTrue,
			RowConditionType: "Ready",
		},
		{
			APIVersions: v1.APIVersions{
				Versions: []string{"v2"},
			},
			GroupKind: v1.GroupKind{
				Group: "helm.toolkit.fluxcd.io",
				Kind:  "HelmRelease",
			},
			Namespace: "default",
			LabelSelector: v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key:      "helm.toolkit.fluxcd.io/name",
						Operator: v1.LabelSelectorOpIn,
						Values:   []string{"platform-mesh-operator-infra-components"},
					},
				},
			},
			ConditionStatus:  v1.ConditionTrue,
			RowConditionType: "Ready",
		},
	},
}
