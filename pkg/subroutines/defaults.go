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
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		Path:              "root:platform-mesh-system",
		Secret:            "account-operator-kubeconfig",
	},
	{
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		Path:              "root:platform-mesh-system",
		Secret:            "rebac-authz-webhook-kubeconfig",
	},
	{
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		Path:              "root:platform-mesh-system",
		Secret:            "security-operator-kubeconfig",
	},
	{
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		Path:              "root:platform-mesh-system",
		Secret:            "kubernetes-graphql-gateway-kubeconfig",
	},
	{
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		Path:              "root:platform-mesh-system",
		Secret:            "extension-manager-operator-kubeconfig",
	},
	{
		Path:   "root:platform-mesh-system",
		Secret: "iam-service-kubeconfig",
	},
	{
		RawPath: ptr.To("/services/contentconfigurations"),
		Secret:  "portal-kubeconfig",
	},
}
var DefaultInitializerConnection = []corev1alpha1.InitializerConnection{
	{
		WorkspaceTypeName: "security",
		Path:              "root",
		Secret:            "security-initializer-kubeconfig",
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
			Versions:  []string{"v2"},
			Group:     "helm.toolkit.fluxcd.io",
			Kind:      "HelmRelease",
			Namespace: "platform-mesh-system",
			LabelSelector: v1.LabelSelector{
				MatchExpressions: []v1.LabelSelectorRequirement{
					{
						Key:      "core.platform-mesh.io/operator-created",
						Operator: v1.LabelSelectorOpIn,
						Values:   []string{"true"},
					},
				},
			},
			ConditionStatus: v1.ConditionTrue,
			ConditionType:   "Ready",
		},
	},
}
