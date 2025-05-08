package subroutines

import (
	"context"
	"fmt"
	"net/url"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
)

func NewProvidersecretSubroutine(
	client client.Client,
	helper KcpHelper,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client:    client,
		kcpHelper: helper,
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

	secret, err := GetSecret(
		r.client, instance.GetAdminSecretName(), instance.GetAdminSecretNamespace(),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	// Determine which provider connections to use based on configuration:
	var providers []corev1alpha1.ProviderConnection
	hasProv := len(instance.Spec.Kcp.ProviderConnections) > 0
	hasExtraProv := len(instance.Spec.Kcp.ExtraProviderConnections) > 0

	switch {
	case !hasProv && !hasExtraProv:
		// Nothing configured -> use default providers
		providers = DefaultProviderConnections
	case !hasProv && hasExtraProv:
		// Only extra providers configured - use default + extra providers
		providers = append(DefaultProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	case hasProv && !hasExtraProv:
		// Only providers configured -> use only specified providers
		providers = instance.Spec.Kcp.ProviderConnections
	default:
		// Both providers and extra providers configured -> use specified + extra providers
		providers = append(instance.Spec.Kcp.ProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	}

	for _, pc := range providers {
		if _, opErr := r.HandleProviderConnection(ctx, instance, pc, secret); opErr != nil {
			log.Error().Err(opErr.Err()).Msg("Failed to handle provider connection")
			return ctrl.Result{}, opErr
		}
	}

	// Only process initializers if no providers are configured
	if !hasProv && !hasExtraProv {
		// Determine which initializer connections to use based on configuration:
		var inits []corev1alpha1.InitializerConnection
		hasInit := len(instance.Spec.Kcp.InitializerConnections) > 0
		hasExtraInit := len(instance.Spec.Kcp.ExtraInitializerConnections) > 0

		switch {
		case !hasInit && !hasExtraInit:
			// Nothing configured -> use default initializers
			inits = DefaultInitializerConnection
		case !hasInit && hasExtraInit:
			// Only extra initializers configured -> use default + extra initializers
			inits = append(DefaultInitializerConnection, instance.Spec.Kcp.ExtraInitializerConnections...)
		case hasInit && !hasExtraInit:
			// Only initializers configured -> use only specified initializers
			inits = instance.Spec.Kcp.InitializerConnections
		default:
			// Both initializers and extra initializers configured -> use specified + extra initializers
			inits = append(instance.Spec.Kcp.InitializerConnections, instance.Spec.Kcp.ExtraInitializerConnections...)
		}

		for _, ic := range inits {
			if _, opErr := r.HandleInitializerConnection(ctx, instance, ic, secret); opErr != nil {
				log.Error().Err(opErr.Err()).Msg("Failed to handle initializer connection")
				return ctrl.Result{}, opErr
			}
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

func (r *ProvidersecretSubroutine) HandleProviderConnection(
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
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
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
		Server:                   fmt.Sprintf("%s://%s/clusters/%s", u.Scheme, u.Host, pc.Path),
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
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.client, providerSecret, func() error {
		providerSecret.Data = map[string][]byte{
			"kubeconfig": kcpConfigBytes,
		}
		return controllerutil.SetOwnerReference(instance, providerSecret, r.client.Scheme())
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	log.Debug().Str("secret", pc.Secret).Msg("Created or updated provider secret")

	return ctrl.Result{}, nil
}

func (r *ProvidersecretSubroutine) HandleInitializerConnection(
	ctx context.Context, instance *corev1alpha1.OpenMFP, ic corev1alpha1.InitializerConnection, adminSecret *corev1.Secret,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	key := instance.Spec.Kcp.AdminSecretRef.Key
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(adminSecret.Data[key])
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse REST config from kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	kcpClient, err := r.kcpHelper.NewKcpClient(restConfig, ic.Path)
	if err != nil {
		log.Error().Err(err).Msg("creating kcp client for initializer")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	wt := &kcptenancyv1alpha.WorkspaceType{}
	if err := kcpClient.Get(ctx, types.NamespacedName{Name: ic.WorkspaceTypeName}, wt); err != nil {
		log.Error().Err(err).Msg("getting WorkspaceType")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	if len(wt.Status.VirtualWorkspaces) == 0 {
		err = fmt.Errorf("no virtual workspaces found in %s", ic.WorkspaceTypeName)
		log.Error().Err(err).Msg("bad WorkspaceType")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	cfg, err := clientcmd.Load(adminSecret.Data[key])
	if err != nil {
		log.Error().Err(err).Msg("loading admin kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}
	curr := cfg.CurrentContext
	cluster := cfg.Contexts[curr].Cluster
	cfg.Clusters[cluster].Server = wt.Status.VirtualWorkspaces[0].URL

	data, err := clientcmd.Write(*cfg)
	if err != nil {
		log.Error().Err(err).Msg("writing modified kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	initializerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ic.Secret,
			Namespace: instance.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, initializerSecret, func() error {
		initializerSecret.Data = map[string][]byte{"kubeconfig": data}
		return controllerutil.SetOwnerReference(instance, initializerSecret, r.client.Scheme())
	})
	if err != nil {
		log.Error().Err(err).Msg("creating/updating initializer Secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	return ctrl.Result{}, nil
}
