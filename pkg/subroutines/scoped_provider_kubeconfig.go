package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpapiv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

const (
	defaultScopedSANamespace       = "default"
	defaultScopedSecretNamespace   = "platform-mesh-system"
	secondsPerDay                  = 86400
	defaultTokenExpirationSeconds  = 7 * secondsPerDay
	scopedClusterRolePrefix        = "platform-mesh-provider-"
	scopedSAPrefix                 = "platform-mesh-provider-"
	kcpWorkspaceAccessRoleName     = "system:kcp:workspace:access"
	scopedProviderRBACNameDefault  = "scoped"
	maxRBACNameSuffixLength        = 200
	platformMeshAPIExportWorkspace = "root:platform-mesh-system"
)

func sanitizeSecretNameForRBAC(secret string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(secret) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.':
			b.WriteRune('-')
		default:
		}
	}
	s := strings.Trim(strings.TrimSpace(b.String()), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > maxRBACNameSuffixLength {
		s = s[:maxRBACNameSuffixLength]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		return scopedProviderRBACNameDefault
	}
	return s
}

func buildKCPConfigForPath(cfg *rest.Config, workspacePath string) *rest.Config {
	out := rest.CopyConfig(cfg)
	schemeHost := cfg.Host
	if cfg.Host != "" {
		base := cfg.Host
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = "https://" + base
		}
		if parsed, err := url.Parse(base); err == nil && parsed.Host != "" {
			schemeHost = parsed.Scheme + "://" + parsed.Host
		}
	}
	out.Host = schemeHost + "/clusters/" + workspacePath
	return out
}

func resolveAPIExport(ctx context.Context, kcpHelper KcpHelper, cfg *rest.Config, apiExportName, apiExportPath string) (*kcpapiv1alpha2.APIExport, error) {
	if apiExportName == "" {
		return nil, fmt.Errorf("cannot resolve APIExport: APIExportName is required")
	}
	if apiExportPath == "" {
		return nil, fmt.Errorf("cannot resolve APIExport: workspace path is required")
	}

	kcpClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), apiExportPath)
	if err != nil {
		return nil, err
	}

	var export kcpapiv1alpha2.APIExport
	if err := kcpClient.Get(ctx, client.ObjectKey{Name: apiExportName}, &export); err != nil {
		return nil, fmt.Errorf("get APIExport %s in workspace %s: %w", apiExportName, apiExportPath, err)
	}
	return &export, nil
}

func getPolicyRulesFromAPIExport(export *kcpapiv1alpha2.APIExport) ([]rbacv1.PolicyRule, error) {
	var rules []rbacv1.PolicyRule

	for _, res := range export.Spec.Resources {
		group := res.Group
		resource := res.Name
		if resource == "" {
			continue
		}
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: []string{resource},
			Verbs:     []string{"*"},
		})
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: []string{resource + "/status"},
			Verbs:     []string{"get", "update", "patch"},
		})
	}

	for _, claim := range export.Spec.PermissionClaims {
		group := claim.Group
		resource := claim.Resource
		if resource == "" {
			continue
		}
		verbs := claim.Verbs
		if len(verbs) == 0 {
			verbs = []string{"*"}
		}
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups: []string{group},
			Resources: []string{resource},
			Verbs:     verbs,
		})
		if hasUpdatePatchVerbs(verbs) {
			rules = append(rules, rbacv1.PolicyRule{
				APIGroups: []string{group},
				Resources: []string{resource + "/status"},
				Verbs:     []string{"get", "update", "patch"},
			})
		}
	}

	if export.ObjectMeta.Name != "" {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups:     []string{"apis.kcp.io"},
			Resources:     []string{"apiexports/content"},
			ResourceNames: []string{export.ObjectMeta.Name},
			Verbs:         []string{"*"},
		})
	}

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{"apis.kcp.io"},
		Resources: []string{"apiexportendpointslices"},
		Verbs:     []string{"get", "list", "watch"},
	})

	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{"apis.kcp.io"},
		Resources: []string{"apibindings"},
		Verbs:     []string{"get", "list", "watch"},
	})

	rules = append(rules, rbacv1.PolicyRule{
		NonResourceURLs: []string{"/api", "/api/*", "/apis", "/apis/*", "/clusters/*"},
		Verbs:           []string{"get"},
	})

	return rules, nil
}

func hasUpdatePatchVerbs(verbs []string) bool {
	for _, v := range verbs {
		if v == "*" || v == "update" || v == "patch" {
			return true
		}
	}
	return false
}

func ensureScopedProviderServiceAccountAndRBAC(ctx context.Context, kcpClient client.Client, policyRules []rbacv1.PolicyRule, providerSuffix string) (saName string, err error) {
	if providerSuffix == "" {
		providerSuffix = scopedProviderRBACNameDefault
	}
	saName = scopedSAPrefix + providerSuffix
	crName := scopedClusterRolePrefix + providerSuffix
	workspaceAccessCRBName := "platform-mesh-workspace-access-" + providerSuffix
	saNamespace := defaultScopedSANamespace

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: saNamespace,
			Name:      saName,
		},
	}
	if err := kcpClient.Create(ctx, sa); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("create ServiceAccount %s: %w", saName, err)
		}
	}

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Rules:      policyRules,
	}
	if err := kcpClient.Create(ctx, cr); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("create ClusterRole %s: %w", crName, err)
		}
		if err := kcpClient.Get(ctx, client.ObjectKey{Name: crName}, cr); err != nil {
			return "", err
		}
		cr.Rules = policyRules
		if err := kcpClient.Update(ctx, cr); err != nil {
			return "", fmt.Errorf("update ClusterRole %s: %w", crName, err)
		}
	}

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, kcpClient, crb, func() error {
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     crName,
		}
		crb.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Namespace: saNamespace,
				Name:      saName,
			},
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("create or update ClusterRoleBinding %s: %w", crName, err)
	}

	workspaceAccessCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: workspaceAccessCRBName},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, kcpClient, workspaceAccessCRB, func() error {
		workspaceAccessCRB.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     kcpWorkspaceAccessRoleName,
		}
		workspaceAccessCRB.Subjects = []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Namespace: saNamespace,
				Name:      saName,
			},
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("create or update ClusterRoleBinding %s for workspace access: %w", workspaceAccessCRBName, err)
	}
	return saName, nil
}

func createTokenForSA(ctx context.Context, cfg *rest.Config, workspacePath, namespace, saName string, expirationSeconds int64) (string, error) {
	config := buildKCPConfigForPath(cfg, workspacePath)
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("create clientset for KCP: %w", err)
	}
	expSec := expirationSeconds
	if expSec <= 0 {
		expSec = defaultTokenExpirationSeconds
	}
	treq := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &expSec,
		},
	}
	tr, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, saName, treq, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create token for ServiceAccount %s/%s: %w", namespace, saName, err)
	}
	return tr.Status.Token, nil
}

// joinVirtualWorkspaceServerURL attaches the path from APIExportEndpointSlice status to the operator-chosen front-proxy base URL.
func joinVirtualWorkspaceServerURL(hostPort, rawPath string) (string, error) {
	return url.JoinPath(hostPort, rawPath)
}

func virtualWorkspacePathFromSlice(slice *kcpapiv1alpha1.APIExportEndpointSlice) (string, error) {
	if slice == nil {
		return "", fmt.Errorf("nil APIExportEndpointSlice")
	}
	if len(slice.Status.APIExportEndpoints) == 0 {
		return "", fmt.Errorf("no endpoints in APIExportEndpointSlice %q", slice.Name)
	}
	raw := slice.Status.APIExportEndpoints[0].URL
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("invalid endpoint URL on APIExportEndpointSlice %q", slice.Name)
	}
	return strings.TrimSuffix(u.Path, "/"), nil
}

// apiExportLocationFromEndpointSlice returns spec.export name and path from the slice object (no trimming).
func apiExportLocationFromEndpointSlice(slice *kcpapiv1alpha1.APIExportEndpointSlice) (apiExportName, exportWorkspacePath string, err error) {
	if slice == nil {
		return "", "", fmt.Errorf("nil APIExportEndpointSlice")
	}
	n := slice.Name
	if slice.Spec.APIExport.Name == "" {
		return "", "", fmt.Errorf("APIExportEndpointSlice %q has empty spec.export.name", n)
	}
	if slice.Spec.APIExport.Path == "" {
		return "", "", fmt.Errorf("APIExportEndpointSlice %q has empty spec.export.path", n)
	}
	return slice.Spec.APIExport.Name, slice.Spec.APIExport.Path, nil
}

// resolveAPIExportVirtualWorkspaceRawPath returns the URL path segment for joining to front-proxy host (admin kubeconfig), from APIExportEndpointSlice status in platformMeshAPIExportWorkspace.
func resolveAPIExportVirtualWorkspaceRawPath(ctx context.Context, kcpHelper KcpHelper, baseCfg *rest.Config, sliceName string) (string, error) {
	if sliceName == "" {
		return "", fmt.Errorf("APIExportEndpointSlice name is empty")
	}
	name := sliceName
	sliceClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(baseCfg), platformMeshAPIExportWorkspace)
	if err != nil {
		return "", fmt.Errorf("kcp client for APIExportEndpointSlice workspace: %w", err)
	}
	var endpointSlice kcpapiv1alpha1.APIExportEndpointSlice
	if err := sliceClient.Get(ctx, client.ObjectKey{Name: name}, &endpointSlice); err != nil {
		return "", fmt.Errorf("get APIExportEndpointSlice %q in %s: %w", name, platformMeshAPIExportWorkspace, err)
	}
	return virtualWorkspacePathFromSlice(&endpointSlice)
}

// parseScopedKubeconfigExportSource validates that exactly one of endpointSliceName or apiExportName is set (after trim).
func parseScopedKubeconfigExportSource(pc corev1alpha1.ProviderConnection) (endpointSliceName, apiExportName string, err error) {
	endpointSliceName = strings.TrimSpace(ptr.Deref(pc.EndpointSliceName, ""))
	apiExportName = strings.TrimSpace(ptr.Deref(pc.APIExportName, ""))
	if endpointSliceName != "" && apiExportName != "" {
		return "", "", fmt.Errorf("scoped kubeconfig: set only one of endpointSliceName or apiExportName")
	}
	if endpointSliceName == "" && apiExportName == "" {
		return "", "", fmt.Errorf("scoped kubeconfig requires endpointSliceName or apiExportName")
	}
	return endpointSliceName, apiExportName, nil
}

// writeScopedKubeconfigToSecret builds a scoped kubeconfig: ServiceAccount token in pc.Path, RBAC from APIExport; server is virtual workspace when endpointSliceName is set, else workspace cluster URL when apiExportName is set.
func writeScopedKubeconfigToSecret(
	ctx context.Context,
	k8sClient client.Client,
	kcpHelper KcpHelper,
	cfg *rest.Config,
	instance *corev1alpha1.PlatformMesh,
	pc corev1alpha1.ProviderConnection,
) error {
	log := logger.LoadLoggerFromContext(ctx)
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	hostPort := fmt.Sprintf("https://%s-front-proxy.%s:%s", operatorCfg.KCP.FrontProxyName, operatorCfg.KCP.Namespace, operatorCfg.KCP.FrontProxyPort)
	if pc.External {
		hostPort = fmt.Sprintf("https://kcp.api.%s:%d", instance.Spec.Exposure.BaseDomain, instance.Spec.Exposure.Port)
	}

	pcPath := strings.TrimSpace(pc.Path)
	if pcPath == "" {
		return fmt.Errorf("scoped kubeconfig requires Path (workspace)")
	}

	secretNamespace := defaultScopedSecretNamespace
	if ptr.Deref(pc.Namespace, "") != "" {
		secretNamespace = *pc.Namespace
	}

	endpointSliceName, apiExportNameField, err := parseScopedKubeconfigExportSource(pc)
	if err != nil {
		return err
	}

	var hostURL string
	var apiExportName string
	var exportWorkspacePath string

	if endpointSliceName != "" {
		sliceClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), platformMeshAPIExportWorkspace)
		if err != nil {
			return errors.Wrap(err, "kcp client for APIExportEndpointSlice workspace")
		}
		var endpointSlice kcpapiv1alpha1.APIExportEndpointSlice
		if err := sliceClient.Get(ctx, client.ObjectKey{Name: endpointSliceName}, &endpointSlice); err != nil {
			return fmt.Errorf("get APIExportEndpointSlice %q in %s: %w", endpointSliceName, platformMeshAPIExportWorkspace, err)
		}
		rawPath, err := virtualWorkspacePathFromSlice(&endpointSlice)
		if err != nil {
			return err
		}

		var aerr error
		apiExportName, exportWorkspacePath, aerr = apiExportLocationFromEndpointSlice(&endpointSlice)
		if aerr != nil {
			return aerr
		}

		hostURL, err = joinVirtualWorkspaceServerURL(hostPort, rawPath)
		if err != nil {
			return errors.Wrap(err, "build scoped virtual workspace server URL")
		}
		log.Info().
			Str("secret", pc.Secret).
			Str("path", pcPath).
			Str("endpointSlice", endpointSliceName).
			Str("apiExport", apiExportName).
			Msg("Using scoped kubeconfig virtual workspace URL")
	} else {
		apiExportName = apiExportNameField
		exportWorkspacePath = platformMeshAPIExportWorkspace
		var errJoin error
		hostURL, errJoin = url.JoinPath(hostPort, "clusters", pcPath)
		if errJoin != nil {
			return errors.Wrap(errJoin, "build scoped workspace cluster server URL")
		}
		log.Info().
			Str("secret", pc.Secret).
			Str("path", pcPath).
			Str("apiExport", apiExportName).
			Msg("Using scoped kubeconfig workspace cluster URL")
	}

	export, err := resolveAPIExport(ctx, kcpHelper, cfg, apiExportName, exportWorkspacePath)
	if err != nil {
		return errors.Wrap(err, "resolve APIExport")
	}
	rules, err := getPolicyRulesFromAPIExport(export)
	if err != nil {
		return errors.Wrap(err, "build RBAC from APIExport")
	}

	caData := cfg.TLSClientConfig.CAData
	if caData == nil {
		caData = []byte{}
	}

	kcpWorkspaceClient, err := kcpHelper.NewKcpClient(rest.CopyConfig(cfg), pcPath)
	if err != nil {
		return errors.Wrap(err, "create KCP client for provider workspace")
	}
	providerSuffix := sanitizeSecretNameForRBAC(pc.Secret)
	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, kcpWorkspaceClient, rules, providerSuffix)
	if err != nil {
		return errors.Wrap(err, "ensure ServiceAccount and RBAC")
	}

	token, err := createTokenForSA(ctx, cfg, pcPath, defaultScopedSANamespace, saName, defaultTokenExpirationSeconds)
	if err != nil {
		return errors.Wrap(err, "create token for ServiceAccount")
	}
	kubeconfig := buildScopedKubeconfig(hostURL, token, caData)
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errors.Wrap(err, "write kubeconfig")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: pc.Secret, Namespace: secretNamespace},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, secret, func() error {
		secret.Data = map[string][]byte{"kubeconfig": kubeconfigBytes}
		return nil
	})
	if err != nil {
		return errors.Wrap(err, "write provider secret")
	}
	return nil
}

func buildScopedKubeconfig(hostURL string, token string, caData []byte) *clientcmdapi.Config {
	return &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default-cluster": {
				Server:                   hostURL,
				CertificateAuthorityData: caData,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default-auth": {
				Token: token,
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
}
