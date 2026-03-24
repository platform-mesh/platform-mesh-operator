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
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	logicalcluster "github.com/kcp-dev/logicalcluster/v3"
)

const AdminKubeconfigSecretName = "kubeconfig-kcp-admin"

const platformMeshAPIExportWorkspace = "root:platform-mesh-system"

type HelmGetter interface {
	GetRelease(ctx context.Context, client client.Client, name, ns string) (*unstructured.Unstructured, error)
}

type DefaultHelmGetter struct{}

func (g DefaultHelmGetter) GetRelease(ctx context.Context, cli client.Client, name, ns string) (*unstructured.Unstructured, error) {
	return getHelmRelease(ctx, cli, name, ns)
}

func NewProviderSecretSubroutine(
	client client.Client,
	helper KcpHelper,
	helm HelmGetter,
	kcpUrl string,
) *ProvidersecretSubroutine {
	return &ProvidersecretSubroutine{
		client:    client,
		kcpHelper: helper,
		helm:      helm,
		kcpUrl:    kcpUrl,
	}
}

type ProvidersecretSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
	helm      HelmGetter
	kcpUrl    string
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

	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	err := r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.RootShardName, Namespace: operatorCfg.KCP.Namespace}, rootShard)
	if err != nil || !matchesConditionWithStatus(rootShard, "Available", "True") {
		log.Info().Msg("RootShard is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("RootShard is not ready"), true, true)
	}

	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	err = r.client.Get(ctx, types.NamespacedName{Name: operatorCfg.KCP.FrontProxyName, Namespace: operatorCfg.KCP.Namespace}, frontProxy)

	if err != nil || !matchesConditionWithStatus(frontProxy, "Available", "True") {
		log.Info().Msg("FrontProxy is not ready..")
		return ctrl.Result{}, errors.NewOperatorError(errors.New("FrontProxy is not ready"), true, true)
	}

	var providers []corev1alpha1.ProviderConnection
	hasProv := len(instance.Spec.Kcp.ProviderConnections) > 0
	hasExtraProv := len(instance.Spec.Kcp.ExtraProviderConnections) > 0

	switch {
	case !hasProv && !hasExtraProv:
		providers = DefaultProviderConnections
	case !hasProv && hasExtraProv:
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
		providers = instance.Spec.Kcp.ProviderConnections
	default:
		providers = append(instance.Spec.Kcp.ProviderConnections, instance.Spec.Kcp.ExtraProviderConnections...)
	}

	if HasFeatureToggle(instance, "feature-enable-terminal-controller-manager") == "true" {
		providers = append(providers, corev1alpha1.ProviderConnection{
			Path:   "root:platform-mesh-system",
			Secret: "terminal-controller-manager-kubeconfig",
		})
	}

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

func effectiveKubeconfigAuth(pc corev1alpha1.ProviderConnection) corev1alpha1.KubeconfigAuth {
	if pc.KubeconfigAuth != "" {
		return pc.KubeconfigAuth
	}
	return corev1alpha1.KubeconfigAuthAdminKubeconfig
}

func scopedExtraPolicyFlags(perms *corev1alpha1.ServiceAccountPermissions) *ExtraPolicyRulesFlags {
	if perms == nil {
		return nil
	}
	if perms.EnableGetLogicalCluster == nil {
		return nil
	}
	return &ExtraPolicyRulesFlags{
		EnableGetLogicalCluster: perms.EnableGetLogicalCluster,
	}
}

func (r *ProvidersecretSubroutine) ensureRootOrgAccess(
	ctx context.Context,
	pc corev1alpha1.ProviderConnection,
	cfg *rest.Config,
) error {
	perms := pc.ServiceAccountPermissions
	if perms == nil || perms.RootOrgAccess == nil || !*perms.RootOrgAccess {
		return nil
	}
	sourceClient, err := r.kcpHelper.NewKcpClient(rest.CopyConfig(cfg), pc.Path)
	if err != nil {
		return fmt.Errorf("create KCP client for %s: %w", pc.Path, err)
	}
	var lc kcpcorev1alpha.LogicalCluster
	if err := sourceClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
		return fmt.Errorf("get LogicalCluster cluster in %s: %w", pc.Path, err)
	}
	clusterID := logicalcluster.From(&lc).String()
	if clusterID == "" {
		return fmt.Errorf("LogicalCluster cluster in %s has no kcp.io/cluster annotation", pc.Path)
	}
	orgsClient, err := r.kcpHelper.NewKcpClient(rest.CopyConfig(cfg), "root:orgs")
	if err != nil {
		return fmt.Errorf("create KCP client for root:orgs: %w", err)
	}
	if err := EnsureRootOrgsAccess(ctx, orgsClient, clusterID); err != nil {
		return fmt.Errorf("ensure root:orgs cluster access: %w", err)
	}
	providerSuffix := sanitizeSecretNameForRBAC(pc.Secret)
	saName := scopedSAPrefix + providerSuffix
	if err := ensureRootOrgsAccessForServiceAccount(ctx, orgsClient, defaultScopedSANamespace, saName, providerSuffix); err != nil {
		return fmt.Errorf("ensure root:orgs serviceaccount access: %w", err)
	}
	if err := r.ensureRootOrgChildrenAccessForServiceAccount(ctx, cfg, saName, providerSuffix, clusterID); err != nil {
		return fmt.Errorf("ensure root:orgs child workspaces serviceaccount access: %w", err)
	}
	return nil
}

func (r *ProvidersecretSubroutine) ensureRootOrgChildrenAccessForServiceAccount(
	ctx context.Context,
	cfg *rest.Config,
	saName string,
	providerSuffix string,
	clusterID string,
) error {
	queue := []string{"root:orgs"}
	seen := map[string]struct{}{"root:orgs": {}}

	for len(queue) > 0 {
		parentPath := queue[0]
		queue = queue[1:]

		parentClient, err := r.kcpHelper.NewKcpClient(rest.CopyConfig(cfg), parentPath)
		if err != nil {
			return fmt.Errorf("create KCP client for %s: %w", parentPath, err)
		}

		var workspaces kcptenancyv1alpha.WorkspaceList
		if err := parentClient.List(ctx, &workspaces); err != nil {
			return fmt.Errorf("list workspaces in %s: %w", parentPath, err)
		}

		for _, ws := range workspaces.Items {
			if ws.Name == "" {
				continue
			}
			childPath := parentPath + ":" + ws.Name
			childClient, err := r.kcpHelper.NewKcpClient(rest.CopyConfig(cfg), childPath)
			if err != nil {
				return fmt.Errorf("create KCP client for %s: %w", childPath, err)
			}

			relPath := strings.TrimPrefix(childPath, "root:orgs:")
			if relPath == childPath {
				relPath = ws.Name
			}
			workspaceSuffix := providerSuffix + "-" + sanitizeSecretNameForRBAC(strings.ReplaceAll(relPath, ":", "-"))
			if err := ensureWorkspaceAccessForServiceAccount(ctx, childClient, defaultScopedSANamespace, saName, workspaceSuffix, clusterID); err != nil {
				return fmt.Errorf("ensure serviceaccount access in %s: %w", childPath, err)
			}

			if _, ok := seen[childPath]; !ok {
				seen[childPath] = struct{}{}
				queue = append(queue, childPath)
			}
		}
	}
	return nil
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

var resolveAPIExportVirtualWorkspaceRawPath = apiExportVirtualWorkspaceRawPathImpl

func SetResolveAPIExportVirtualWorkspaceRawPathForTesting(
	fn func(context.Context, *rest.Config, string) (string, error),
) (restore func()) {
	prev := resolveAPIExportVirtualWorkspaceRawPath
	resolveAPIExportVirtualWorkspaceRawPath = fn
	return func() { resolveAPIExportVirtualWorkspaceRawPath = prev }
}

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
	switch auth {
	case corev1alpha1.KubeconfigAuthAdminKubeconfig:
		return r.writeProviderSecretFromAdminKubeconfigForConnection(ctx, hostPort, pc, cfg)
	case corev1alpha1.KubeconfigAuthServiceAccountScoped:
		apiExportName := strings.TrimSpace(pc.APIExportName)
		if apiExportName == "" && pc.EndpointSliceName != nil {
			apiExportName = strings.TrimSpace(*pc.EndpointSliceName)
		}
		if apiExportName == "" {
			err := fmt.Errorf("kubeconfigAuth %q requires apiExportName or endpointSliceName", auth)
			log.Error().Err(err).Str("secret", pc.Secret).Msg("Invalid provider connection configuration")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}
		if err := r.ensureRootOrgAccess(ctx, pc, cfg); err != nil {
			log.Warn().Err(err).Str("secret", pc.Secret).Msg("Failed to ensure root:orgs access for scoped provider")
		}
		spec := ProviderConnectionSpec{
			Path:                  pc.Path,
			Secret:                pc.Secret,
			APIExportName:         apiExportName,
			ExtraPolicyRuleBlocks: scopedExtraPolicyFlags(pc.ServiceAccountPermissions),
		}
		if err := WriteScopedKubeconfigToSecret(ctx, r.client, cfg, spec, hostPort, providerConnectionNamespace(pc)); err != nil {
			log.Error().Err(err).Str("secret", pc.Secret).Msg("Failed to write scoped provider kubeconfig")
			return ctrl.Result{}, errors.NewOperatorError(err, true, false)
		}
		return ctrl.Result{}, nil
	default:
		err := fmt.Errorf("unsupported kubeconfigAuth %q", auth)
		log.Error().Err(err).Str("secret", pc.Secret).Msg("Provider connection uses unsupported kubeconfigAuth")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
}

func buildHostURLForScoped(hostPort, workspacePath string) (string, error) {
	return url.JoinPath(hostPort, "clusters", workspacePath)
}

func buildServerURLFromAdminKubeconfig(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	hostPort string,
	workspacePath string,
) (string, error) {
	adminKubeconfigData, err := loadAdminKubeconfigFromSecret(ctx, k8sClient, namespace)
	if err != nil {
		return "", err
	}
	cfg, err := clientcmd.Load(adminKubeconfigData)
	if err != nil {
		return "", fmt.Errorf("load admin kubeconfig: %w", err)
	}
	if workspacePath != "" {
		return buildHostURLForScoped(hostPort, workspacePath)
	}
	baseURL, err := url.Parse(hostPort)
	if err != nil {
		return "", fmt.Errorf("parse hostPort %q: %w", hostPort, err)
	}
	for _, c := range cfg.Clusters {
		if c == nil {
			continue
		}
		parsed, parseErr := url.Parse(c.Server)
		if parseErr != nil || parsed.Host == "" {
			return hostPort, nil
		}
		parsed.Scheme = baseURL.Scheme
		parsed.Host = baseURL.Host
		return parsed.String(), nil
	}
	return hostPort, nil
}

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
