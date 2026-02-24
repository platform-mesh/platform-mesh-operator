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
var AccountOperatorWorkspace = "root:platform-mesh-system"
var DefaultProviderConnections = []corev1alpha1.ProviderConnection{
	{
		Path:               "root:platform-mesh-system",
		Secret:             "account-operator-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		Path:               "root:platform-mesh-system",
		Secret:             "rebac-authz-webhook-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
		APIExportName:      "core.platform-mesh.io",
	},
	{
		Path:               "root:platform-mesh-system",
		Secret:             "security-operator-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		EndpointSliceName:  ptr.To("core.platform-mesh.io"),
		Path:               "root:platform-mesh-system",
		Secret:             "kubernetes-grapqhl-gateway-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		Path:               "root:platform-mesh-system",
		Secret:             "extension-manager-operator-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
		APIExportName:      "core.platform-mesh.io",
	},
	{
		Path:               "root:platform-mesh-system",
		Secret:             "iam-service-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		RawPath:            ptr.To("/services/contentconfigurations"),
		Secret:             "portal-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		Path:               "root",
		Secret:             "security-initializer-kubeconfig",
		UseAdminKubeconfig: ptr.To(false),
	},
	{
		Path:   "root",
		Secret: "security-terminator-kubeconfig",
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
