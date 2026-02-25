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
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
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

	// Build kcp kubeonfig
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}
	var failed []struct {
		secret string
		err    error
	}
	for _, pc := range providers {
		if _, opErr := r.HandleProviderConnection(ctx, instance, pc, cfg); opErr != nil {
			log.Error().Err(opErr.Err()).Str("secret", pc.Secret).Msg("Failed to handle provider connection")
			failed = append(failed, struct {
				secret string
				err    error
			}{pc.Secret, opErr.Err()})
		}
	}
	if res, opErr := providerConnectionFailuresResult(failed); opErr != nil {
		return res, opErr
	}
	return ctrl.Result{}, nil
}

// providerConnectionFailuresResult returns a combined error and requeue result for failed provider connections.
// If failed is empty it returns (ctrl.Result{}, nil).
func providerConnectionFailuresResult(failed []struct {
	secret string
	err    error
}) (ctrl.Result, errors.OperatorError) {
	if len(failed) == 0 {
		return ctrl.Result{}, nil
	}
	var msg strings.Builder
	msg.WriteString("provider connection(s) failed: ")
	for i, f := range failed {
		if i > 0 {
			msg.WriteString("; ")
		}
		msg.WriteString(f.secret)
		msg.WriteString(": ")
		msg.WriteString(f.err.Error())
	}
	return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("%s", msg.String()), true, false)
}

func (r *ProvidersecretSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{ProvidersecretSubroutineFinalizer}
}

func (r *ProvidersecretSubroutine) GetName() string {
	return ProvidersecretSubroutineName
}

// normalizeRestConfigHost ensures cfg.Host has a scheme so that url.Parse(cfg.Host) yields a valid host (e.g. "example.com" -> "https://example.com").
func normalizeRestConfigHost(cfg *rest.Config) {
	if cfg == nil || cfg.Host == "" {
		return
	}
	if !strings.HasPrefix(cfg.Host, "http://") && !strings.HasPrefix(cfg.Host, "https://") {
		cfg.Host = "https://" + cfg.Host
	}
}

func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	normalizeRestConfigHost(cfg)

	hostPort := fmt.Sprintf("https://%s-front-proxy.%s:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort)
	if pc.External && instance != nil && instance.Spec.Exposure != nil && instance.Spec.Exposure.BaseDomain != "" {
		baseDomain := instance.Spec.Exposure.BaseDomain
		port := instance.Spec.Exposure.Port
		if port == 0 {
			port = 443
		}
		// Platform convention: KCP API is exposed at kcp.api.<baseDomain> (see Istio host, portal-server-lib; docs/ANALYSIS_PORTAL_URL_AND_KCP_API_PREFIX.md).
		if strings.Contains(baseDomain, ":") {
			hostPort = "https://kcp.api." + baseDomain
		} else {
			hostPort = fmt.Sprintf("https://kcp.api.%s:%d", baseDomain, port)
		}
	}
	hasBaseURL := operatorCfg.KCP.FrontProxyName != "" || (pc.External && instance != nil && instance.Spec.Exposure != nil && instance.Spec.Exposure.BaseDomain != "")

	if !ptr.Deref(pc.UseAdminKubeconfig, false) && !hasBaseURL {
		log.Error().Msg("Scoped kubeconfig requested but no base URL available (FrontProxy not set and external exposure not set)")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("scoped kubeconfig requested but no base URL"), true, true)
	}

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

		if !ptr.Deref(pc.UseAdminKubeconfig, false) && hasBaseURL && (ptr.Deref(pc.EndpointSliceName, "") != "" || pc.APIExportName != "") {
			res, opErr := r.writeScopedKubeconfigWithSlice(ctx, pc, cfg, hostPort)
			if opErr != nil {
				return res, opErr
			}
			log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
			return ctrl.Result{}, nil
		}

		endpointURL := slice.Status.APIExportEndpoints[0].URL
		address, err = url.Parse(endpointURL)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse endpoint URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
	} else {
		if !ptr.Deref(pc.UseAdminKubeconfig, false) && hasBaseURL && pc.APIExportName != "" {
			res, opErr := r.writeScopedKubeconfigWithoutSlice(ctx, pc, cfg, hostPort)
			if opErr != nil {
				return res, opErr
			}
			log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
			return ctrl.Result{}, nil
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

	vwURL, err := url.Parse(wt.Status.VirtualWorkspaces[0].URL)
	if err != nil {
		log.Error().Err(err).Msg("parsing virtual workspace URL")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	if operatorCfg.KCP.FrontProxyName != "" {
		vwURL.Host = fmt.Sprintf("%s-front-proxy:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.FrontProxyPort)
		log.Debug().Str("url", vwURL.String()).Msg("modified virtual workspace URL (operator front-proxy)")
	} else {
		// Fallback: use the same host as the operator's KCP config (restCfg.Host).
		if baseURL, parseErr := url.Parse(restCfg.Host); parseErr == nil {
			vwURL.Host = baseURL.Host
		}
		log.Debug().Str("url", vwURL.String()).Msg("modified virtual workspace URL (restCfg host)")
	}
	apiConfig.Clusters[cluster].Server = vwURL.String()

	data, err := clientcmd.Write(*apiConfig)
	if err != nil {
		log.Error().Err(err).Msg("writing modified kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	namespace := "platform-mesh-system"
	if ic.Namespace != "" {
		namespace = ic.Namespace
	}
	initializerSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ic.Secret,
			Namespace: namespace,
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

// writeScopedKubeconfigWithSlice creates a scoped kubeconfig when we have an APIExportEndpointSlice and writes the secret.
func (r *ProvidersecretSubroutine) writeScopedKubeconfigWithSlice(
	ctx context.Context,
	pc corev1alpha1.ProviderConnection,
	cfg *rest.Config,
	hostPort string,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	apiExportName := pc.APIExportName
	if apiExportName == "" {
		apiExportName = *pc.EndpointSliceName
	}
	namespace := DefaultScopedSecretNamespace
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	err := WriteScopedKubeconfigToSecret(ctx, r.client, cfg, ProviderConnectionSpec{
		Path:          pc.Path,
		Secret:        pc.Secret,
		APIExportName: apiExportName,
	}, hostPort, namespace)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create scoped kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
	return ctrl.Result{}, nil
}

// writeScopedKubeconfigWithoutSlice creates a scoped kubeconfig when we have no endpoint slice but have APIExportName.
func (r *ProvidersecretSubroutine) writeScopedKubeconfigWithoutSlice(
	ctx context.Context,
	pc corev1alpha1.ProviderConnection,
	cfg *rest.Config,
	hostPort string,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	namespace := DefaultScopedSecretNamespace
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	err := WriteScopedKubeconfigToSecret(ctx, r.client, cfg, ProviderConnectionSpec{
		Path:          pc.Path,
		Secret:        pc.Secret,
		APIExportName: pc.APIExportName,
	}, hostPort, namespace)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create scoped kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
	return ctrl.Result{}, nil
}
