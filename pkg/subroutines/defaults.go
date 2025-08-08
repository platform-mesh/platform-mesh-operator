package subroutines

import corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"

var AccountOperatorWebhookSecretName = "account-operator-webhook-server-cert"
var AccountOperatorWebhookSecretNamespace = "openmfp-system"

var DefaultCASecretKey = "ca.crt"
var AccountOperatorMutatingWebhookName = "account-operator.webhooks.core.platform-mesh.io"
var AccountOperatorValidatingWebhookName = "organization-validator.webhooks.core.platform-mesh.io"
var AccountOperatorWorkspace = "root:openmfp-system"
var DefaultProviderConnections = []corev1alpha1.ProviderConnection{
	{
		EndpointSliceName: "core.platform-mesh.io",
		Path:              "root:openmfp-system",
		Secret:            "account-operator-kubeconfig",
	},
	{
		EndpointSliceName: "core.platform-mesh.io",
		Path:              "root:openmfp-system",
		Secret:            "rebac-authz-webhook-kubeconfig",
	},
	{
		EndpointSliceName: "core.platform-mesh.io",
		Path:              "root:openmfp-system",
		Secret:            "security-operator-kubeconfig",
	},
	{
		EndpointSliceName: "core.platform-mesh.io",
		Path:              "root:openmfp-system",
		Secret:            "kubernetes-grapqhl-gateway-kubeconfig",
	},
	{
		EndpointSliceName: "core.platform-mesh.io",
		Path:              "root:openmfp-system",
		Secret:            "extension-manager-operator-kubeconfig",
	},
	{
		EndpointSliceName: "",
		Path:              "/services/contentconfigurations/",
		Secret:            "portal-kubeconfig",
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
