package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
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
	ProvidersecretSubroutineFinalizer = "platform-mesh.core.platform-mesh.io/finalizer"
)

func (r *ProvidersecretSubroutine) Finalize(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *ProvidersecretSubroutine) Process(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	scheme := r.client.Scheme()
	if scheme == nil {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("client scheme is nil"), true, false)
	}

	instance := runtimeObj.(*corev1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx)

	// Wait for kcp release to be ready before continuing
	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	// Wait for root shard to be ready
	err := r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("RootShard is not ready"), true, true)
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	// Wait for root shard to be ready
	err = r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)

	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("FrontProxy is not ready"), true, true)
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

	if HasFeatureToggle(instance, "feature-enable-terminal-controller-manager") == "true" {
		providers = append(providers, corev1alpha1.ProviderConnection{
			Path:   "root:platform-mesh-system",
			Secret: "terminal-controller-manager-kubeconfig",
		})
	}

	// Build kcp kubeonfig
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}
	var errs []error
	for _, pc := range providers {
		if _, opErr := r.HandleProviderConnection(ctx, instance, pc, cfg); opErr != nil {
			log.Error().Err(opErr.Err()).Str("secret", pc.Secret).Msg("Failed to handle provider connection")
			errs = append(errs, fmt.Errorf("%s: %w", pc.Secret, opErr.Err()))
		}
	}
	if len(errs) > 0 {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("provider connection(s) failed: %w", utilerrors.NewAggregate(errs)), true, false)
	}
	return ctrl.Result{}, nil
}

func (r *ProvidersecretSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

// normalizeRestConfigHost ensures cfg.Host has a scheme so that url.Parse(cfg.Host) yields a valid host (e.g. "example.com" -> "https://example.com").
// scheme is the protocol from the PlatformMesh exposure (see baseDomainPortProtocol), or "https" when not set.
func normalizeRestConfigHost(cfg *rest.Config, scheme string) {
	if cfg == nil || cfg.Host == "" {
		return
	}
	if !strings.HasPrefix(cfg.Host, "http://") && !strings.HasPrefix(cfg.Host, "https://") {
		if scheme == "" {
			scheme = "https"
		}
		cfg.Host = scheme + "://" + cfg.Host
	}
}

func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	_, _, _, scheme := baseDomainPortProtocol(instance)
	normalizeRestConfigHost(cfg, scheme)

	hostPort := fmt.Sprintf("%s://%s-front-proxy.%s:%s", scheme, operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort)
	if pc.External && instance != nil && instance.Spec.Exposure != nil && instance.Spec.Exposure.BaseDomain != "" {
		exp := instance.Spec.Exposure
		baseDomain := exp.BaseDomain
		port := exp.Port
		if port == 0 {
			port = 443
		}
		// BaseDomain is the KCP API host (no hardcoded prefix; may be e.g. "kcp.api.example.com" or "kcp.example.com").
		if strings.Contains(baseDomain, ":") {
			hostPort = scheme + "://" + baseDomain
		} else {
			hostPort = fmt.Sprintf("%s://%s:%d", scheme, baseDomain, port)
		}
	}
	hasBaseURL := operatorCfg.KCP.FrontProxyName != "" || (pc.External && instance != nil && instance.Spec.Exposure != nil && instance.Spec.Exposure.BaseDomain != "")

	var address *url.URL

	if ptr.Deref(pc.EndpointSliceName, "") != "" {
		kcpClient, err := r.kcpHelper.NewKcpClient(cfg, pc.Path)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create KCP client")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}

		var slice kcpapiv1alpha.APIExportEndpointSlice
		err = kcpClient.Get(ctx, client.ObjectKey{Name: *pc.EndpointSliceName}, &slice)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get APIExportEndpointSlice")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}

		if len(slice.Status.APIExportEndpoints) == 0 {
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("no endpoints in slice"), true, false)
		}

		// Scoped kubeconfig only when UseAdminKubeconfig is explicitly false; default (nil/true) stays admin.
		if pc.UseAdminKubeconfig != nil && !*pc.UseAdminKubeconfig {
			log.Info().Str("secret", pc.Secret).Msg("Creating scoped kubeconfig for provider connection (endpoint slice)")
			return r.writeScopedKubeconfig(ctx, pc, cfg, hostPort, hasBaseURL)
		}

		endpointURL := slice.Status.APIExportEndpoints[0].URL
		address, err = url.Parse(endpointURL)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse endpoint URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
	} else {
		if pc.UseAdminKubeconfig != nil && !*pc.UseAdminKubeconfig {
			if pc.APIExportName == "" {
				err := fmt.Errorf("scoped kubeconfig (useAdminKubeconfig: false) requires apiExportName or endpointSliceName")
				log.Error().Err(err).Str("secret", pc.Secret).Msg("Invalid provider connection configuration")
				return ctrl.Result{}, errors.NewOperatorError(err, false, false)
			}
			log.Info().Str("secret", pc.Secret).Str("apiExportName", pc.APIExportName).Msg("Creating scoped kubeconfig for provider connection")
			return r.writeScopedKubeconfig(ctx, pc, cfg, hostPort, hasBaseURL)
		}

		kcpUrl, err := url.Parse(cfg.Host)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse KCP URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
		if ptr.Deref(pc.RawPath, "") != "" {
			kcpUrl.Path = *pc.RawPath
		} else {
			kcpUrl.Path = path.Join("clusters", pc.Path)
		}
		address = kcpUrl
	}

	newConfig := rest.CopyConfig(cfg)
	host, err := url.JoinPath(hostPort, address.Path)
	if err != nil {
		log.Error().Err(err).Msg("Failed to join path for provider connection")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	newConfig.Host = host

	apiConfig := restConfigToAPIConfig(newConfig)
	kcpConfigBytes, err := clientcmd.Write(*apiConfig)
	if err != nil {
		log.Error().Err(err).Msg("Failed to write kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	namespace := "platform-mesh-system"
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	providerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pc.Secret,
			Namespace: namespace,
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

// writeScopedKubeconfig creates a scoped kubeconfig for the provider connection and writes the secret.
func (r *ProvidersecretSubroutine) writeScopedKubeconfig(
	ctx context.Context,
	pc corev1alpha1.ProviderConnection,
	cfg *rest.Config,
	hostPort string,
	hasBaseURL bool,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	if !hasBaseURL {
		log.Error().Msg("Scoped kubeconfig requested but no base URL available (FrontProxy not set and external exposure not set)")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("scoped kubeconfig requested but no base URL"), true, true)
	}
	apiExportName := pc.APIExportName
	if apiExportName == "" && pc.EndpointSliceName != nil {
		apiExportName = *pc.EndpointSliceName
	}
	namespace := defaultScopedSecretNamespace
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	if err := WriteScopedKubeconfigToSecret(ctx, r.client, cfg, ProviderConnectionSpec{
		Path:          pc.Path,
		Secret:        pc.Secret,
		APIExportName: apiExportName,
	}, hostPort, namespace); err != nil {
		log.Error().Err(err).Msg("Failed to create scoped kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
	return ctrl.Result{}, nil
}
