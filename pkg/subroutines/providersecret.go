package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

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

const AdminKubeconfigSecretName = "kubeconfig-kcp-admin"

// AdminKubeconfigSecretName is the secret created by kcp-operator with key "kubeconfig".
// For adminKubeconfig auth it is used as the source kubeconfig for credentials and cluster entries.

// HelmGetter is an interface for getting Helm releases.
type HelmGetter interface {
	GetRelease(ctx context.Context, client client.Client, name, ns string) (*unstructured.Unstructured, error)
}

// DefaultHelmGetter is the default implementation of HelmGetter
type DefaultHelmGetter struct{}

// GetRelease implements HelmGetter interface
func (g DefaultHelmGetter) GetRelease(ctx context.Context, cli client.Client, name, ns string) (*unstructured.Unstructured, error) {
	return getHelmRelease(ctx, cli, name, ns)
}

func NewProviderSecretSubroutine(client client.Client, kcpUrl string) *ProvidersecretSubroutine {
	return &ProvidersecretSubroutine{
		client: client,
		kcpUrl: kcpUrl,
	}
}

type ProvidersecretSubroutine struct {
	client client.Client
	kcpUrl string
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

// Process builds the KCP kubeconfig, ensures root:orgs RBAC when any provider uses admin kubeconfig auth,
// then writes a provider secret per connection using the kcp admin secret.
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
	// Wait for front proxy to be ready
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
			Path:   "root:platform-mesh-system",
			Secret: "terminal-controller-manager-kubeconfig",
		})
	}

	// Build KCP kubeconfig.
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}
	ensureRootOrgsAccessIfAdminKubeconfigUsed(ctx, cfg, providers, instance)

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

// normalizeRestConfigHost ensures cfg.Host includes a scheme.
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

// effectiveKubeconfigAuth returns the auth mode; default is KubeconfigAuthAdminKubeconfig.
func effectiveKubeconfigAuth(pc corev1alpha1.ProviderConnection) string {
	if pc.KubeconfigAuth != "" {
		return pc.KubeconfigAuth
	}
	return corev1alpha1.KubeconfigAuthAdminKubeconfig
}

// ensureRootOrgsAccessIfAdminKubeconfigUsed ensures root:orgs RBAC once when any provider uses adminKubeconfig auth (idempotent).
func ensureRootOrgsAccessIfAdminKubeconfigUsed(ctx context.Context, cfg *rest.Config, providers []corev1alpha1.ProviderConnection, instance *corev1alpha1.PlatformMesh) {
	log := logger.LoadLoggerFromContext(ctx)
	_, _, _, scheme := baseDomainPortProtocol(instance)
	normalizeRestConfigHost(cfg, scheme)
	for _, pc := range providers {
		if effectiveKubeconfigAuth(pc) == corev1alpha1.KubeconfigAuthAdminKubeconfig {
			if err := EnsureRootOrgsAccessForProviderWorkspace(ctx, cfg); err != nil {
				log.Warn().Err(err).Msg("EnsureRootOrgsAccessForProviderWorkspace failed (e.g. rebac not ready); root:orgs RBAC will be applied on next reconcile")
			} else {
				log.Info().Msg("Ensured root:orgs access for provider workspace")
			}
			break
		}
	}
}

func providerConnectionNamespace(pc corev1alpha1.ProviderConnection) string {
	namespace := "platform-mesh-system"
	if ptr.Deref(pc.Namespace, "") != "" {
		namespace = *pc.Namespace
	}
	return namespace
}

func loadAdminKubeconfigFromSecret(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
) ([]byte, error) {
	adminSecret, err := GetSecret(k8sClient, AdminKubeconfigSecretName, namespace)
	if err != nil {
		return nil, fmt.Errorf("read %s/%s: %w", namespace, AdminKubeconfigSecretName, err)
	}
	if adminSecret == nil || len(adminSecret.Data["kubeconfig"]) == 0 {
		return nil, fmt.Errorf("secret %s/%s missing key kubeconfig", namespace, AdminKubeconfigSecretName)
	}
	return adminSecret.Data["kubeconfig"], nil
}

// writeProviderSecretFromAdminKubeconfigForConnection loads the admin kubeconfig secret and writes the provider secret.
func (r *ProvidersecretSubroutine) writeProviderSecretFromAdminKubeconfigForConnection(
	ctx context.Context, hostPort string, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	adminKubeconfigData, err := loadAdminKubeconfigFromSecret(ctx, r.client, operatorCfg.KCP.Namespace)
	if err != nil {
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to read kubeconfig-kcp-admin for provider")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	if err := writeProviderSecretFromAdminKubeconfig(
		ctx,
		r.client,
		adminKubeconfigData,
		hostPort,
		pc.Path,
		ptr.Deref(pc.RawPath, ""),
		cfg.CAData,
		pc.Secret,
		providerConnectionNamespace(pc),
	); err != nil {
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write provider secret from kubeconfig-kcp-admin")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	return ctrl.Result{}, nil
}

// HandleProviderConnection writes the provider secret for one connection using the kcp admin secret.
// Only KubeconfigAuthAdminKubeconfig is supported; certificate auth is no longer supported.
func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	_, _, _, scheme := baseDomainPortProtocol(instance)
	normalizeRestConfigHost(cfg, scheme)

	auth := effectiveKubeconfigAuth(pc)
	if auth != corev1alpha1.KubeconfigAuthAdminKubeconfig {
		err := fmt.Errorf("unsupported kubeconfigAuth %q; only %s is supported", auth, corev1alpha1.KubeconfigAuthAdminKubeconfig)
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Provider connection uses unsupported auth")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	hostPort := fmt.Sprintf("%s://%s-front-proxy.%s:%s", scheme, operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort)
	if pc.External {
		externalURL, err := url.Parse(cfg.Host)
		if err == nil && externalURL.Scheme != "" && externalURL.Host != "" {
			effectiveScheme := scheme
			if effectiveScheme == "" {
				effectiveScheme = externalURL.Scheme
			}
			hostPort = effectiveScheme + "://" + externalURL.Host
		}
	}

	return r.writeProviderSecretFromAdminKubeconfigForConnection(ctx, hostPort, pc, cfg)
}

func buildHostURLForScoped(hostPort, workspacePath string) (string, error) {
	return url.JoinPath(hostPort, "clusters", workspacePath)
}

// writeProviderSecretFromAdminKubeconfig writes the provider secret using the given admin kubeconfig
// (e.g. from secret kubeconfig-kcp-admin).
//
// Important: prefer the provider's configured target path (RawPath or Path) over the source kubeconfig path.
// This keeps provider secrets aligned with PlatformMesh ProviderConnection configuration while still
// using kubeconfig-kcp-admin as source for credentials and CA data.
func writeProviderSecretFromAdminKubeconfig(ctx context.Context, k8sClient client.Client, kubeconfigData []byte, hostPort, workspacePath, rawPath string, frontProxyCAData []byte, providerSecretName, providerSecretNamespace string) error {
	cfg, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return fmt.Errorf("load admin kubeconfig: %w", err)
	}

	baseURL, err := url.Parse(hostPort)
	if err != nil {
		return fmt.Errorf("parse hostPort %q: %w", hostPort, err)
	}

	targetServerURL := ""
	if rawPath != "" {
		targetServerURL, err = url.JoinPath(hostPort, rawPath)
		if err != nil {
			return fmt.Errorf("build server URL from rawPath %q: %w", rawPath, err)
		}
	} else if workspacePath != "" {
		targetServerURL, err = buildHostURLForScoped(hostPort, workspacePath)
		if err != nil {
			return fmt.Errorf("build server URL for workspace path %q: %w", workspacePath, err)
		}
	}

	for name, c := range cfg.Clusters {
		if c == nil {
			continue
		}
		if len(frontProxyCAData) > 0 {
			c.CertificateAuthorityData = frontProxyCAData
			c.CertificateAuthority = ""
			c.InsecureSkipTLSVerify = false
		}
		if targetServerURL != "" {
			c.Server = targetServerURL
			continue
		}

		parsedServer, err := url.Parse(c.Server)
		if err != nil || parsedServer.Host == "" {
			// Fallback for malformed or host-less entries: use hostPort directly when no target path is configured.
			if workspacePath != "" {
				serverURL, buildErr := buildHostURLForScoped(hostPort, workspacePath)
				if buildErr != nil {
					return fmt.Errorf("build fallback server URL for cluster %q: %w", name, buildErr)
				}
				c.Server = serverURL
			} else {
				c.Server = hostPort
			}
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
