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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	logicalcluster "github.com/kcp-dev/logicalcluster/v3"
)

const AdminKubeconfigSecretName = "kubeconfig-kcp-admin"

// Workspace that hosts platform-mesh APIExports (e.g. core.platform-mesh.io).
const platformMeshAPIExportWorkspace = "root:platform-mesh-system"

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

// Process builds the KCP kubeconfig and writes a provider secret per connection using the kcp admin secret.
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

// resolveAPIExportVirtualWorkspaceRawPath builds /services/apiexport/<cluster>/<export> for kubeconfig server paths.
// Overridden in tests via export_test.go.
var resolveAPIExportVirtualWorkspaceRawPath = apiExportVirtualWorkspaceRawPathImpl

func apiExportVirtualWorkspaceRawPathImpl(ctx context.Context, baseCfg *rest.Config, sliceOrExportName string) (string, error) {
	name := strings.TrimSpace(sliceOrExportName)
	if name == "" {
		return "", fmt.Errorf("APIExportEndpointSlice/APIExport name is empty")
	}
	h := Helper{}

	kcpCl, err := h.NewKcpClient(rest.CopyConfig(baseCfg), platformMeshAPIExportWorkspace)
	if err != nil {
		return "", fmt.Errorf("kcp client for %s: %w", platformMeshAPIExportWorkspace, err)
	}

	export := kcpapiv1alpha.APIExport{}
	exportErr := kcpCl.Get(ctx, types.NamespacedName{Name: name}, &export)
	if exportErr == nil {
		for _, vw := range export.Status.VirtualWorkspaces {
			if p, ok := parseValidAPIExportVirtualWorkspaceURL(vw.URL, name); ok {
				return p, nil
			}
		}
	}

	var endpointSlice kcpapiv1alpha.APIExportEndpointSlice
	sliceErr := kcpCl.Get(ctx, types.NamespacedName{Name: name}, &endpointSlice)
	if sliceErr == nil {
		for _, ep := range endpointSlice.Status.APIExportEndpoints {
			if p, ok := parseValidAPIExportVirtualWorkspaceURL(ep.URL, name); ok {
				return p, nil
			}
		}
	} else if sliceErr != nil && !apierrors.IsNotFound(sliceErr) {
		return "", fmt.Errorf("get APIExportEndpointSlice %q in %s: %w", name, platformMeshAPIExportWorkspace, sliceErr)
	}

	if p, ok := tryAPIExportVirtualWorkspacePathFromWorkspace(ctx, &h, baseCfg, platformMeshAPIExportWorkspace, name); ok {
		return p, nil
	}

	if exportErr != nil {
		if sliceErr != nil && apierrors.IsNotFound(sliceErr) {
			return "", fmt.Errorf("get APIExport %q in %s: %w", name, platformMeshAPIExportWorkspace, exportErr)
		}
		return "", fmt.Errorf("get APIExport %q in %s: %w", name, platformMeshAPIExportWorkspace, exportErr)
	}
	if export.Status.IdentityHash != "" && logicalcluster.Name(export.Status.IdentityHash).IsValid() {
		return fmt.Sprintf("/services/apiexport/%s/%s", export.Status.IdentityHash, name), nil
	}
	return "", fmt.Errorf("APIExport %q: no usable virtual-workspace path (workspace cluster, endpoint URL, or valid identityHash)", name)
}

func parentWorkspacePath(workspacePath string) (parent, leaf string, ok bool) {
	p := strings.TrimSpace(workspacePath)
	if p == "" {
		return "", "", false
	}
	i := strings.LastIndex(p, ":")
	if i <= 0 || i == len(p)-1 {
		return "", "", false
	}
	parent, leaf = p[:i], p[i+1:]
	if parent == "" || leaf == "" {
		return "", "", false
	}
	return parent, leaf, true
}

func tryAPIExportVirtualWorkspacePathFromWorkspace(
	ctx context.Context, h *Helper, baseCfg *rest.Config, exportWorkspacePath, exportName string,
) (string, bool) {
	parent, leaf, ok := parentWorkspacePath(exportWorkspacePath)
	if !ok {
		return "", false
	}
	parentCl, err := h.NewKcpClient(rest.CopyConfig(baseCfg), parent)
	if err != nil {
		return "", false
	}
	var ws kcptenancyv1alpha.Workspace
	if err := parentCl.Get(ctx, types.NamespacedName{Name: leaf}, &ws); err != nil {
		return "", false
	}
	seg := strings.TrimSpace(ws.Spec.Cluster)
	if seg == "" || !logicalcluster.Name(seg).IsValid() {
		return "", false
	}
	return fmt.Sprintf("/services/apiexport/%s/%s", seg, exportName), true
}

func parseValidAPIExportVirtualWorkspaceURL(rawURL, exportName string) (string, bool) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" || parsed.Path == "/" {
		return "", false
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	const pfx = "/services/apiexport/"
	if !strings.HasPrefix(path, pfx) {
		return "", false
	}
	rest := strings.TrimPrefix(path, pfx)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	clusterSeg, exportSeg := parts[0], parts[1]
	if exportSeg != exportName {
		return "", false
	}
	if !logicalcluster.Name(clusterSeg).IsValid() {
		return "", false
	}
	return pfx + clusterSeg + "/" + exportSeg, true
}

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

	workspacePath := pc.Path
	rawPath := ptr.Deref(pc.RawPath, "")
	if rawPath == "" && pc.EndpointSliceName != nil {
		if name := strings.TrimSpace(*pc.EndpointSliceName); name != "" {
			rp, err := resolveAPIExportVirtualWorkspaceRawPath(ctx, cfg, name)
			if err != nil {
				log.Warn().Err(err).Str("secret", pc.Secret).Str("apiExport", name).Msg("APIExport path not ready, requeue")
				return ctrl.Result{}, errors.NewOperatorError(err, true, true)
			}
			rawPath = rp
			workspacePath = ""
		}
	}

	if err := writeProviderSecretFromAdminKubeconfig(
		ctx,
		r.client,
		adminKubeconfigData,
		hostPort,
		workspacePath,
		rawPath,
		cfg.CAData,
		pc.Secret,
		providerConnectionNamespace(pc),
	); err != nil {
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write provider secret from kubeconfig-kcp-admin")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	return ctrl.Result{}, nil
}

// HandleProviderConnection writes one provider secret from kubeconfig-kcp-admin. cfg is for export path resolution and optional CA override.
func (r *ProvidersecretSubroutine) HandleProviderConnection(
	ctx context.Context, instance *corev1alpha1.PlatformMesh, pc corev1alpha1.ProviderConnection, cfg *rest.Config,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)
	_, _, _, scheme := baseDomainPortProtocol(instance)
	normalizeRestConfigHost(cfg, scheme)

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

	auth := effectiveKubeconfigAuth(pc)
	if auth != corev1alpha1.KubeconfigAuthAdminKubeconfig {
		err := fmt.Errorf("unsupported kubeconfigAuth %q; only %s is supported (secret %s)",
			auth, corev1alpha1.KubeconfigAuthAdminKubeconfig, AdminKubeconfigSecretName)
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Provider connection uses unsupported kubeconfigAuth")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	return r.writeProviderSecretFromAdminKubeconfigForConnection(ctx, hostPort, pc, cfg)
}

func buildHostURLForScoped(hostPort, workspacePath string) (string, error) {
	return url.JoinPath(hostPort, "clusters", workspacePath)
}

// writeProviderSecretFromAdminKubeconfig copies auth from admin kubeconfig and sets cluster server from Path/RawPath.
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
		// Non-empty frontProxyCAData: trust front-proxy CA; else keep CA from kubeconfig-kcp-admin.
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
