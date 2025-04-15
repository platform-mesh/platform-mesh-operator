package subroutines

import (
	"context"
	"fmt"

	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	v1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
)

type WebhooksSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
}

const (
	WebhooksSubroutineName      = "WebhooksSubroutine"
	WebhooksSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

func NewWebhooksSubroutine(client client.Client, helper KcpHelper) *WebhooksSubroutine {
	sub := WebhooksSubroutine{
		client: client,
	}
	if helper == nil {
		sub.kcpHelper = &Helper{}
	} else {
		sub.kcpHelper = helper
	}
	return &sub
}

func (r *WebhooksSubroutine) GetName() string {
	return WebhooksSubroutineName
}

// TODO: Implement the following methods
func (r *WebhooksSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *WebhooksSubroutine) Finalizers() []string { // coverage-ignore
	return []string{WebhooksSubroutineFinalizer}
}

func (r *WebhooksSubroutine) Process(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	instance := runtimeObj.(*corev1alpha1.OpenMFP)

	// default webhook configuration
	if len(instance.Spec.Kcp.WebhookConfigurations) == 0 {
		config := corev1alpha1.WebhookConfiguration{
			SecretRef: corev1alpha1.SecretReference{
				Name:      WEBHOOK_DEFAULT_K8S_SECRET_NAME,
				Namespace: WEBHOOK_DEFAULT_K8S_SECRET_NAMESPACE,
			},
			SecretData: WEBHOOK_DEFAULT_K8S_SECRET_DATA,
			WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
				ApiVersion: "admissionregistration.k8s.io/v1",
				Kind:       "MutatingWebhookConfiguration",
				Name:       WEBHOOK_DEFAULT_KCP_WEBHOOK_NAME,
				Path:       WEBHOOK_DEFAULT_KCP_PATH,
			},
		}
		res, err := r.handleWebhookConfig(ctx, instance, config)
		if err != nil {
			log.Error().Err(err.Err()).Msg("Error handling webhook configuration")
			return res, err
		}
	}

	for _, webhookConfig := range instance.Spec.Kcp.WebhookConfigurations {
		res, err := r.handleWebhookConfig(ctx, instance, webhookConfig)
		if err != nil {
			log.Error().Err(err.Err()).Msg("Error handling webhook configuration")
			return res, err
		}
	}

	for _, webhookConfig := range instance.Spec.Kcp.ExtraWebhookConfigurations {
		res, err := r.handleWebhookConfig(ctx, instance, webhookConfig)
		if err != nil {
			log.Error().Err(err.Err()).Msg("Error handling webhook configuration")
			return res, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *WebhooksSubroutine) handleWebhookConfig(
	ctx context.Context, instance *corev1alpha1.OpenMFP, webhookConfig corev1alpha1.WebhookConfiguration,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	kcpAdminSecret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      instance.GetAdminSecretName(),
		Namespace: instance.GetAdminSecretNamespace(),
	}, &kcpAdminSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(kcpAdminSecret.Data[instance.GetAdminSecretKey()])
	if err != nil {
		log.Error().Err(err).Msg("Failed to get kubeconfig from secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	kcpclient, err := r.kcpHelper.NewKcpClient(config, webhookConfig.WebhookRef.Path)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create kcp client")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	caSecret := corev1.Secret{}
	err = r.client.Get(ctx, types.NamespacedName{
		Name:      webhookConfig.SecretRef.Name,
		Namespace: webhookConfig.SecretRef.Namespace,
	}, &caSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get ca secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	caData, ok := caSecret.Data[webhookConfig.SecretData]
	if !ok {
		log.Error().Msg("Failed to get caData from secret")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("Failed to get caData from secret"), false, false)
	}

	webhook := v1.MutatingWebhookConfiguration{}
	err = kcpclient.Get(ctx, types.NamespacedName{Name: webhookConfig.WebhookRef.Name}, &webhook)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get webhook configuration")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	webhook.Webhooks[0].ClientConfig.CABundle = caData

	unstructuredWH, err := convertToUnstructured(webhook, caData)
	if err != nil {
		log.Error().Err(err).Msg("Failed to convert webhook to unstructured")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	err = kcpclient.Patch(ctx, unstructuredWH, client.Apply, client.FieldOwner("openmfp-operator"))
	if err != nil {
		log.Error().Err(err).Msg("Failed to update webhook configuration")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	log.Debug().Msg("Successfully updated webhook's caData")

	return ctrl.Result{}, nil
}

func convertToUnstructured(webhook v1.MutatingWebhookConfiguration, caData []byte) (*unstructured.Unstructured, error) {
	// Convert the structured object to a map
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&webhook)
	if err != nil {
		return nil, err
	}
	// Create an unstructured object and assign the map
	unstructuredObj := &unstructured.Unstructured{Object: objMap}
	unstructuredObj.SetKind("MutatingWebhookConfiguration")
	unstructuredObj.SetAPIVersion("admissionregistration.k8s.io/v1")
	unstructuredObj.SetManagedFields(nil)
	return unstructuredObj, nil
}
