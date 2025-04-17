package subroutines

import (
	"context"
	"fmt"
	"net/url"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func NewProvidersecretSubroutine(
	client client.Client,
	helper KcpHelper,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client: client,
	}
	if helper == nil {
		sub.kcpHelper = &Helper{}
	} else {
		sub.kcpHelper = helper
	}
	return sub
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
}

const (
	ProvidersecretSubroutineName      = "ProvidersecretSubroutine"
	ProvidersecretSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

// TODO: Implement the following methods
func (r *ProvidersecretSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *ProvidersecretSubroutine) Process(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {

	instance := runtimeObj.(*corev1alpha1.OpenMFP)

	log := logger.LoadLoggerFromContext(ctx)

	secret, err := r.kcpHelper.GetSecret(
		r.client, instance.GetAdminSecretName(), instance.GetAdminSecretNamespace(),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	if len(instance.Spec.Kcp.ProviderConnections) == 0 {
		log.Info().Msg("Applying default provider connection")
		defaultProviderConnection := DefaultProviderConnection
		_, errOp := r.handleProviderConnection(ctx, instance, defaultProviderConnection, secret)
		if errOp != nil {
			log.Error().Err(errOp.Err()).Msg("Failed to handle default provider-connection")
			return ctrl.Result{}, errOp
		}
	}

	for _, pc := range instance.Spec.Kcp.ProviderConnections {
		_, errOp := r.handleProviderConnection(ctx, instance, pc, secret)
		if errOp != nil {
			log.Error().Err(errOp.Err()).Msg("Failed to handle provider connection")
			return ctrl.Result{}, errOp
		}
	}
	for _, pc := range instance.Spec.Kcp.ExtraProviderConnections {
		_, errOp := r.handleProviderConnection(ctx, instance, pc, secret)
		if errOp != nil {
			log.Error().Err(errOp.Err()).Msg("Failed to handle extra provider connection")
			return ctrl.Result{}, errOp
		}
	}

	return ctrl.Result{}, nil
}

// TODO: Implement the following methods
func (r *ProvidersecretSubroutine) Finalizers() []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

func (r *ProvidersecretSubroutine) handleProviderConnection(
	ctx context.Context, instance *corev1alpha1.OpenMFP, pc corev1alpha1.ProviderConnection, secret *corev1.Secret,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	secretKey := instance.GetAdminSecretKey()

	kcpConfig, err := clientcmd.Load(secret.Data[secretKey])
	if err != nil {
		log.Error().Err(err).Msg("Failed to load kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	kcpConfigBytes := secret.Data[secretKey]
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kcpConfigBytes)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse REST config from kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	kcpClient, err := r.kcpHelper.NewKcpClient(restConfig, pc.Path)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create KCP client")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	var slice kcpapiv1alpha.APIExportEndpointSlice
	err = kcpClient.Get(ctx, client.ObjectKey{Name: pc.EndpointSliceName}, &slice)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get APIExportEndpointSlice")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	if len(slice.Status.APIExportEndpoints) == 0 {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("no endpoints in slice"), true, false)
	}

	endpointURL := slice.Status.APIExportEndpoints[0].URL
	currentContextName := kcpConfig.CurrentContext
	currentContext, ok := kcpConfig.Contexts[currentContextName]
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("context %s not found in kubeconfig", currentContextName), false, false)
	}

	clusterName := currentContext.Cluster
	u, err := url.Parse(endpointURL)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse endpoint URL")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	existingCluster, ok := kcpConfig.Clusters[clusterName]
	if !ok {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("cluster %s not found in kubeconfig", clusterName), false, false)
	}

	kcpConfig.Clusters[clusterName] = &clientcmdapi.Cluster{
		Server:                   fmt.Sprintf("%s://%s/%s", u.Scheme, u.Host, pc.Path),
		CertificateAuthorityData: existingCluster.CertificateAuthorityData,
	}

	kcpConfigBytes, err = clientcmd.Write(*kcpConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pc.Secret,
			Namespace: instance.Namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": kcpConfigBytes,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.client, providerSecret, func() error {
		return controllerutil.SetOwnerReference(instance, providerSecret, r.client.Scheme())
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	return ctrl.Result{}, nil
}
