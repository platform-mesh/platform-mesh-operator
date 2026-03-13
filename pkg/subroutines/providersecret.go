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
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
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

// AdminKubeconfigSecretName is the secret created by kcp-operator with key "kubeconfig".
// For serviceAccountAdmin, we use it as source of truth for the server path (e.g. /clusters/root),
// while still using an admin SA token for authentication.
const AdminKubeconfigSecretName = "kubeconfig-kcp-admin"

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
		// Only extra providers configured - use default + extra providers.
		// Skip default entries whose secret name appears in extra so the extra (e.g. serviceAccountAdmin) wins.
		extraSecretNames := make(map[string]struct{})
		for _, pc := range instance.Spec.Kcp.ExtraProviderConnections {
			extraSecretNames[pc.Secret] = struct{}{}
		}
		for _, pc := range DefaultProviderConnections {
			if _, inExtra := extraSecretNames[pc.Secret]; !inExtra {
				providers = append(providers, pc)
			}
		}
		providers = append(providers, instance.Spec.Kcp.ExtraProviderConnections...)
	case hasProv && !hasExtraProv:
		// Only providers configured -> use only specified providers
		providers = instance.Spec.Kcp.ProviderConnections
	default:
		// Both providers and extra providers configured -> use specified + extra providers
		providers = append(instance.Spec.Kcp.ProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	}

	if HasFeatureToggle(instance, "feature-enable-terminal-controller-manager") == "true" {
		providers = append(providers, corev1alpha1.ProviderConnection{
			Path:           "root:platform-mesh-system",
			Secret:         "terminal-controller-manager-kubeconfig",
			KubeconfigAuth: corev1alpha1.KubeconfigAuthServiceAccountScoped,
			APIExportName:  "core.platform-mesh.io",
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

// effectiveKubeconfigAuth returns the effective auth mode for the provider connection.
// When KubeconfigAuth is set, it is used. Otherwise defaults to adminCertificate.
func effectiveKubeconfigAuth(pc corev1alpha1.ProviderConnection) string {
	if pc.KubeconfigAuth != "" {
		return pc.KubeconfigAuth
	}
	return corev1alpha1.KubeconfigAuthAdminCertificate
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

	auth := effectiveKubeconfigAuth(pc)
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

		switch auth {
		case corev1alpha1.KubeconfigAuthServiceAccountScoped:
			if err := r.ensureRootOrgAccess(ctx, pc, cfg); err != nil {
				log.Warn().Err(err).Str("secret", pc.Secret).Msg("ensureRootOrgAccess failed (e.g. rebac not ready); creating scoped kubeconfig anyway so provider can start; root:orgs RBAC will be applied on next reconcile")
			} else {
				log.Info().Str("secret", pc.Secret).Msg("Ensured root:orgs access for provider")
			}
			log.Info().Str("secret", pc.Secret).Msg("Creating scoped kubeconfig for provider connection (endpoint slice)")
			return r.writeScopedKubeconfig(ctx, pc, cfg, hostPort, hasBaseURL)
		case corev1alpha1.KubeconfigAuthServiceAccountAdmin:
			if !hasBaseURL {
				log.Error().Msg("Admin kubeconfig requested but no base URL available")
				return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("admin kubeconfig requires base URL"), true, true)
			}
			namespace := "platform-mesh-system"
			if ptr.Deref(pc.Namespace, "") != "" {
				namespace = *pc.Namespace
			}
			enableGetLogicalCluster := false
			if pc.ServiceAccountPermissions != nil && pc.ServiceAccountPermissions.EnableGetLogicalCluster != nil {
				enableGetLogicalCluster = *pc.ServiceAccountPermissions.EnableGetLogicalCluster
			}
			serverURL, buildErr := url.JoinPath(hostPort, address.Path)
			if buildErr != nil {
				log.Error().Err(buildErr).Str("secret", pc.Secret).Msg("Failed to build admin SA server URL from endpoint slice")
				return ctrl.Result{}, errors.NewOperatorError(buildErr, false, false)
			}
			if err := WriteAdminSAKubeconfigToSecretWithServerURL(ctx, r.client, cfg, pc.Path, pc.Secret, serverURL, namespace, enableGetLogicalCluster); err != nil {
				log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write provider secret from admin ServiceAccount kubeconfig")
				return ctrl.Result{}, errors.NewOperatorError(err, true, false)
			}
			log.Info().Str("secret", pc.Secret).Str("path", pc.Path).Str("serverURL", serverURL).Msg("Created or updated provider secret from admin ServiceAccount kubeconfig (endpoint slice)")
			return ctrl.Result{}, nil
		default:
			// adminCertificate: use slice path with hostPort below
		}

		endpointURL := slice.Status.APIExportEndpoints[0].URL
		address, err = url.Parse(endpointURL)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse endpoint URL")
			return ctrl.Result{}, errors.NewOperatorError(err, false, false)
		}
	} else {
		switch auth {
		case corev1alpha1.KubeconfigAuthServiceAccountScoped:
			if pc.APIExportName == "" {
				err := fmt.Errorf("kubeconfigAuth serviceAccountScoped requires apiExportName or endpointSliceName")
				log.Error().Err(err).Str("secret", pc.Secret).Msg("Invalid provider connection configuration")
				return ctrl.Result{}, errors.NewOperatorError(err, false, false)
			}
			if err := r.ensureRootOrgAccess(ctx, pc, cfg); err != nil {
				log.Warn().Err(err).Str("secret", pc.Secret).Msg("ensureRootOrgAccess failed (e.g. rebac not ready); creating scoped kubeconfig anyway so provider can start; root:orgs RBAC will be applied on next reconcile")
			} else {
				log.Info().Str("secret", pc.Secret).Msg("Ensured root:orgs access for provider")
			}
			log.Info().Str("secret", pc.Secret).Str("apiExportName", pc.APIExportName).Msg("Creating scoped kubeconfig for provider connection")
			return r.writeScopedKubeconfig(ctx, pc, cfg, hostPort, hasBaseURL)
		case corev1alpha1.KubeconfigAuthServiceAccountAdmin:
			if !hasBaseURL {
				log.Error().Msg("Admin kubeconfig requested but no base URL available")
				return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("admin kubeconfig requires base URL"), true, true)
			}
			namespace := "platform-mesh-system"
			if ptr.Deref(pc.Namespace, "") != "" {
				namespace = *pc.Namespace
			}
			enableGetLogicalCluster := false
			if pc.ServiceAccountPermissions != nil && pc.ServiceAccountPermissions.EnableGetLogicalCluster != nil {
				enableGetLogicalCluster = *pc.ServiceAccountPermissions.EnableGetLogicalCluster
			}
			operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
			serverURL, serverErr := BuildServerURLFromAdminKubeconfig(ctx, r.client, operatorCfg.KCP.Namespace, hostPort, pc.Path)
			if serverErr != nil {
				log.Warn().Err(serverErr).Str("secret", pc.Secret).Msg("Failed to derive server URL from kubeconfig-kcp-admin; falling back to scoped path URL")
				serverURL = ""
			}
			if serverURL == "" {
				fallbackURL, buildErr := BuildHostURLForScoped(hostPort, pc.Path)
				if buildErr != nil {
					log.Error().Err(buildErr).Str("secret", pc.Secret).Msg("Failed to build fallback admin SA server URL")
					return ctrl.Result{}, errors.NewOperatorError(buildErr, false, false)
				}
				serverURL = fallbackURL
			}
			if err := WriteAdminSAKubeconfigToSecretWithServerURL(ctx, r.client, cfg, pc.Path, pc.Secret, serverURL, namespace, enableGetLogicalCluster); err != nil {
				log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write provider secret from admin ServiceAccount kubeconfig")
				return ctrl.Result{}, errors.NewOperatorError(err, true, false)
			}
			log.Info().Str("secret", pc.Secret).Str("path", pc.Path).Str("serverURL", serverURL).Msg("Created or updated provider secret from admin ServiceAccount kubeconfig")
			return ctrl.Result{}, nil
		default:
			// adminCertificate: build address from path
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
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update secret")
		return ctrl.Result{}, errors.NewOperatorError(err, false, false)
	}

	log.Debug().Str("secret", pc.Secret).Msg("Created or updated provider secret")

	return ctrl.Result{}, nil
}

// BuildServerURLFromAdminKubeconfig reads kubeconfig-kcp-admin and returns the server URL with
// host/scheme rewritten to hostPort while preserving the original path (e.g. /clusters/root).
func BuildServerURLFromAdminKubeconfig(ctx context.Context, k8sClient client.Client, namespace, hostPort, workspacePath string) (string, error) {
	adminSecret, err := GetSecret(k8sClient, AdminKubeconfigSecretName, namespace)
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", namespace, AdminKubeconfigSecretName, err)
	}
	if adminSecret == nil || len(adminSecret.Data["kubeconfig"]) == 0 {
		return "", fmt.Errorf("secret %s/%s missing key kubeconfig", namespace, AdminKubeconfigSecretName)
	}
	cfg, err := clientcmd.Load(adminSecret.Data["kubeconfig"])
	if err != nil {
		return "", fmt.Errorf("load admin kubeconfig: %w", err)
	}
	baseURL, err := url.Parse(hostPort)
	if err != nil {
		return "", fmt.Errorf("parse hostPort %q: %w", hostPort, err)
	}

	// Prefer the server of current-context cluster from kubeconfig-kcp-admin (usually /clusters/root),
	// then named "default", then any valid cluster entry.
	orderedClusterNames := make([]string, 0, len(cfg.Clusters)+2)
	if cfg.CurrentContext != "" {
		if ctxRef, ok := cfg.Contexts[cfg.CurrentContext]; ok && ctxRef != nil && ctxRef.Cluster != "" {
			orderedClusterNames = append(orderedClusterNames, ctxRef.Cluster)
		}
	}
	if _, ok := cfg.Clusters["default"]; ok {
		orderedClusterNames = append(orderedClusterNames, "default")
	}
	for name := range cfg.Clusters {
		orderedClusterNames = append(orderedClusterNames, name)
	}

	seen := map[string]struct{}{}
	for _, name := range orderedClusterNames {
		if _, done := seen[name]; done {
			continue
		}
		seen[name] = struct{}{}
		c := cfg.Clusters[name]
		if c == nil || c.Server == "" {
			continue
		}
		parsedServer, parseErr := url.Parse(c.Server)
		if parseErr != nil || parsedServer.Host == "" {
			continue
		}
		parsedServer.Scheme = baseURL.Scheme
		parsedServer.Host = baseURL.Host
		return parsedServer.String(), nil
	}

	// Fallback if kubeconfig has no valid cluster entries.
	fallbackURL, buildErr := BuildHostURLForScoped(hostPort, workspacePath)
	if buildErr != nil {
		return "", fmt.Errorf("build fallback server URL: %w", buildErr)
	}
	return fallbackURL, nil
}

// writeProviderSecretFromAdminKubeconfig writes the provider secret using the given admin kubeconfig
// (e.g. from secret kubeconfig-kcp-admin).
//
// Important: keep the path from the source kubeconfig and only rewrite scheme/host to front-proxy.
// The kcp-operator secret contains a full kubeconfig (typically /clusters/root); replacing only
// hostname follows that source-of-truth and avoids changing cluster/workspace path semantics.
func writeProviderSecretFromAdminKubeconfig(ctx context.Context, k8sClient client.Client, kubeconfigData []byte, hostPort, workspacePath, providerSecretName, providerSecretNamespace string) error {
	cfg, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return fmt.Errorf("load admin kubeconfig: %w", err)
	}

	baseURL, err := url.Parse(hostPort)
	if err != nil {
		return fmt.Errorf("parse hostPort %q: %w", hostPort, err)
	}

	for name, c := range cfg.Clusters {
		if c == nil {
			continue
		}

		parsedServer, err := url.Parse(c.Server)
		if err != nil || parsedServer.Host == "" {
			// Fallback for malformed or host-less entries: build a deterministic workspace URL.
			serverURL, buildErr := BuildHostURLForScoped(hostPort, workspacePath)
			if buildErr != nil {
				return fmt.Errorf("build fallback server URL for cluster %q: %w", name, buildErr)
			}
			c.Server = serverURL
			continue
		}

		parsedServer.Scheme = baseURL.Scheme
		parsedServer.Host = baseURL.Host
		c.Server = parsedServer.String()
	}

	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: providerSecretName, Namespace: providerSecretNamespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, secret, func() error {
		secret.Data = map[string][]byte{"kubeconfig": out}
		return nil
	})
	return err
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
// ensureRootOrgAccess ensures RBAC in root:orgs for this provider's scoped identity when RootOrgAccess is true.
func (r *ProvidersecretSubroutine) ensureRootOrgAccess(ctx context.Context, pc corev1alpha1.ProviderConnection, cfg *rest.Config) error {
	perms := pc.ServiceAccountPermissions
	if perms == nil || perms.RootOrgAccess == nil || !*perms.RootOrgAccess {
		return nil
	}
	sourceClient, err := r.kcpHelper.NewKcpClient(cfg, pc.Path)
	if err != nil {
		return fmt.Errorf("create KCP client for %s: %w", pc.Path, err)
	}
	var lc kcpcorev1alpha.LogicalCluster
	if err := sourceClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
		return fmt.Errorf("get LogicalCluster cluster in %s: %w", pc.Path, err)
	}
	// KCP uses the name from annotation kcp.io/cluster (logicalcluster.From) for system:cluster:<id>.
	clusterID := logicalcluster.From(&lc).String()
	if clusterID == "" {
		return fmt.Errorf("LogicalCluster cluster in %s has no kcp.io/cluster annotation; required for root:orgs RBAC (system:cluster:<id>)", pc.Path)
	}
	orgsClient, err := r.kcpHelper.NewKcpClient(cfg, "root:orgs")
	if err != nil {
		return fmt.Errorf("create KCP client for root:orgs: %w", err)
	}
	return EnsureRootOrgsAccess(ctx, orgsClient, clusterID)
}

// writeScopedKubeconfig builds a scoped kubeconfig for pc and writes it to a Secret.
// Data flow: CR (pc) + defaults → ProviderConnectionSpec → WriteScopedKubeconfigToSecret
// (resolve APIExport → SA + RBAC in Path workspace → token → kubeconfig → Secret).
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
	spec := ProviderConnectionSpec{
		Path:          pc.Path,
		Secret:        pc.Secret,
		APIExportName: apiExportName,
	}
	if perms := pc.ServiceAccountPermissions; perms != nil {
		hasExtraRules := perms.EnableGetLogicalCluster != nil || perms.EnableStoresAccess != nil || perms.EnableInitTargetsAccess != nil
		if hasExtraRules {
			spec.ExtraPolicyRuleBlocks = &ExtraPolicyRulesFlags{
				EnableGetLogicalCluster: perms.EnableGetLogicalCluster,
				EnableStoresAccess:      perms.EnableStoresAccess,
				EnableInitTargetsAccess: perms.EnableInitTargetsAccess,
			}
		}
	}
	if err := WriteScopedKubeconfigToSecret(ctx, r.client, cfg, spec, hostPort, namespace); err != nil {
		log.Error().Err(err).Msg("Failed to create scoped kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	log.Info().Str("secret", pc.Secret).Msg("Created or updated provider secret (scoped kubeconfig)")
	return ctrl.Result{}, nil
}
