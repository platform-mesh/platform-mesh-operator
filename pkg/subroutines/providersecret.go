package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/errors"
	"github.com/openmfp/golang-commons/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
)

// HelmGetter is an interface for getting Helm releases
type HelmGetter interface {
	GetRelease(ctx context.Context, client client.Client, name, ns string) (*unstructured.Unstructured, error)
}

// DefaultHelmGetter is the default implementation of HelmGetter
type DefaultHelmGetter struct{}

// GetRelease implements HelmGetter interface
func (g DefaultHelmGetter) GetRelease(ctx context.Context, cli client.Client, name, ns string) (*unstructured.Unstructured, error) {
	return getHelmRelease(ctx, cli, name, ns)
}

func NewProviderSecretSubroutine(
	client client.Client,
	helper KcpHelper,
	helm HelmGetter,
	kcpUrl string,
) *ProvidersecretSubroutine {
	sub := &ProvidersecretSubroutine{
		client:    client,
		kcpUrl:    kcpUrl,
		kcpHelper: helper,
		helm:      helm,
	}
	return sub
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
	kcpUrl    string
	helm      HelmGetter
}

const (
	ProvidersecretSubroutineName      = "ProvidersecretSubroutine"
	ProvidersecretSubroutineFinalizer = "openmfp.core.openmfp.org/finalizer"
)

func (r *ProvidersecretSubroutine) Finalize(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *ProvidersecretSubroutine) Process(
	ctx context.Context, runtimeObj lifecycle.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	scheme := r.client.Scheme()
	if scheme == nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("client scheme is nil"), true, false)
	}

	instance := runtimeObj.(*corev1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx)

	// Wait for kcp release to be ready before continuing
	rel, err := r.helm.GetRelease(ctx, r.client, "kcp", "default")
	if err != nil {
		log.Error().Err(err).Msg("Failed to get KCP Release")
		return ctrl.Result{}, errors.NewOperatorError(err, false, true)
	}

	if !isReady(rel) {
		log.Info().Msg("KCP Release is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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

	// Build kcp kubeonfig
	cfg, err := buildKubeconfig(r.client, r.kcpUrl, "kcp-cluster-admin-client-cert")
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}
	for _, pc := range providers {
		if _, opErr := r.HandleProviderConnection(ctx, instance, pc, cfg); opErr != nil {
			log.Error().Err(opErr.Err()).Msg("Failed to handle provider connection")
			return ctrl.Result{}, opErr
		}
	}

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
		if _, opErr := r.HandleInitializerConnection(ctx, instance, ic, cfg); opErr != nil {
			log.Error().Err(opErr.Err()).Msg("Failed to handle initializer connection")
			return ctrl.Result{}, opErr
		}
	}

	return ctrl.Result{}, nil
}

func (r *ProvidersecretSubroutine) Finalizers() []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	var u *url.URL

	if pc.EndpointSliceName != "" {
		kcpClient, err := r.kcpHelper.NewKcpClient(cfg, pc.Path)
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
		u, err = url.Parse(endpointURL)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse endpoint URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
	} else {
		kcpUrl, err := url.Parse(cfg.Host)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse KCP URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		if pc.RawPath != "" {
			kcpUrl.Path = pc.RawPath
		} else {
			kcpUrl.Path = path.Join("/clusters", pc.Path)
		}
		u = kcpUrl
	}

	newConfig := rest.CopyConfig(cfg)
	if pc.External {
		newConfig.Host = fmt.Sprintf("%s://%s%s/", u.Scheme, u.Host, u.Path)
	} else {
		newConfig.Host = fmt.Sprintf("https://kcp-front-proxy.openmfp-system:8443%s/", u.Path)
	}

	apiConfig := restConfigToAPIConfig(newConfig)
	kcpConfigBytes, err := clientcmd.Write(*apiConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pc.Secret,
			Namespace: "openmfp-system",
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.client, providerSecret, func() error {
		providerSecret.Data = map[string][]byte{
			"kubeconfig": kcpConfigBytes,
		}
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	log.Debug().Str("secret", pc.Secret).Msg("Created or updated provider secret")

	return ctrl.Result{}, nil
}

func (r *ProvidersecretSubroutine) HandleInitializerConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, ic corev1alpha1.InitializerConnection, restCfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)

	kcpClient, err := r.kcpHelper.NewKcpClient(restCfg, ic.Path)
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

	newConfig := rest.CopyConfig(restCfg)
	apiConfig := restConfigToAPIConfig(newConfig)
	curr := apiConfig.CurrentContext
	cluster := apiConfig.Contexts[curr].Cluster
	apiConfig.Clusters[cluster].Server = wt.Status.VirtualWorkspaces[0].URL

	data, err := clientcmd.Write(*apiConfig)
	if err != nil {
		log.Error().Err(err).Msg("writing modified kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	initializerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ic.Secret,
			Namespace: "openmfp-system",
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, initializerSecret, func() error {
		initializerSecret.Data = map[string][]byte{"kubeconfig": data}
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("creating/updating initializer Secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	return ctrl.Result{}, nil
}

func restConfigToAPIConfig(restCfg *rest.Config) *clientcmdapi.Config {
	if restCfg == nil {
		return nil
	}

	clientConfig := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default-cluster": {
				Server:                   restCfg.Host,
				CertificateAuthorityData: restCfg.CAData,
				InsecureSkipTLSVerify:    restCfg.Insecure,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default-auth": {
				Token:                 restCfg.BearerToken,
				ClientCertificateData: restCfg.CertData,
				ClientKeyData:         restCfg.KeyData,
				Username:              restCfg.Username,
				Password:              restCfg.Password,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default-context": {
				Cluster:  "default-cluster",
				AuthInfo: "default-auth",
			},
		},
		CurrentContext: "default-context",
	}

	return clientConfig
}
