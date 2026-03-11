package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpapiv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/platform-mesh/golang-commons/errors"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// defaultScopedSANamespace is the default namespace in the KCP workspace where we create ServiceAccounts for scoped kubeconfigs.
	defaultScopedSANamespace = "default"
	// defaultScopedSecretNamespace is the default namespace in the management cluster where provider kubeconfig secrets are written.
	defaultScopedSecretNamespace = "platform-mesh-system"
	// defaultTokenExpirationSeconds is used when TokenExpirationSeconds is not set (7 days).
	secondsPerDay                 = 86400
	defaultTokenExpirationSeconds = 7 * secondsPerDay
	// scopedClusterRolePrefix is the prefix for ClusterRoles created from APIExport.
	scopedClusterRolePrefix = "platform-mesh-provider-"
	// scopedSAPrefix is the prefix for ServiceAccounts created for providers.
	scopedSAPrefix = "platform-mesh-provider-"
	// kcpWorkspaceAccessRoleName is the pre-defined KCP ClusterRole that grants workspace content access (verb=access to "/").
	kcpWorkspaceAccessRoleName = "system:kcp:workspace:access"
	// scopedProviderRBACNameDefault is the default suffix when no provider-specific name is used (e.g. tests).
	scopedProviderRBACNameDefault = "scoped"
	// maxRBACNameSuffixLength limits the SA/CR name suffix so full names stay within K8s limits (253 chars). Prefix is 23 chars.
	maxRBACNameSuffixLength = 200
	// adminSAWorkspacePath is the KCP workspace where the admin ServiceAccount is created. Same level as admin cert (root-level identity).
	adminSAWorkspacePath = "root"
)

// sanitizeSecretNameForRBAC returns a string safe for use in K8s resource names (RFC 1123 subdomain: lowercase, alphanumeric, dash).
// Used to derive per-provider SA/ClusterRole names so each provider has its own identity and rules can be fine-grained later.
func sanitizeSecretNameForRBAC(secret string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(secret) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == '.':
			b.WriteRune('-')
		default:
			// skip other chars
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

// buildKCPConfigForPath returns a copy of cfg with Host set to the KCP workspace path (for use when creating resources in that path).
func buildKCPConfigForPath(cfg *rest.Config, workspacePath string) *rest.Config {
	out := rest.CopyConfig(cfg)
	schemeHost, err := URLSchemeHost(cfg.Host)
	if err != nil {
		// Fallback to original Host so callers still get a valid config
		schemeHost = cfg.Host
	}
	out.Host = schemeHost + "/clusters/" + workspacePath
	return out
}

// newKCPClientWithRBAC returns a controller-runtime client that can talk to the KCP workspace and has APIExport (v1alpha1 + v1alpha2), core.kcp.io (LogicalCluster), core/v1, and rbac/v1 in the scheme.
func newKCPClientWithRBAC(cfg *rest.Config, workspacePath string) (client.Client, error) {
	config := buildKCPConfigForPath(cfg, workspacePath)
	config.QPS = 1000.0
	config.Burst = 2000.0
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha2.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha.AddToScheme(scheme))
	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create KCP client for path %s: %w", workspacePath, err)
	}
	return cl, nil
}

// resolveAPIExport returns the APIExport (v1alpha2).
// Tries v1alpha2 first; if the server only has v1alpha1 (e.g. manifest uses apis.kcp.io/v1alpha1), falls back to v1alpha1.
// apiExportPath must be non-empty (callers pass Path from the provider connection).
func resolveAPIExport(ctx context.Context, cfg *rest.Config, apiExportName, apiExportPath string) (*kcpapiv1alpha2.APIExport, error) {
	if apiExportName == "" {
		return nil, fmt.Errorf("cannot resolve APIExport: APIExportName is required")
	}
	if apiExportPath == "" {
		return nil, fmt.Errorf("cannot resolve APIExport: workspace path is required")
	}

	kcpClient, err := newKCPClientWithRBAC(cfg, apiExportPath)
	if err != nil {
		return nil, err
	}

	var exportV1alpha2 kcpapiv1alpha2.APIExport
	err = kcpClient.Get(ctx, client.ObjectKey{Name: apiExportName}, &exportV1alpha2)
	if err == nil {
		return &exportV1alpha2, nil
	}
	// Fall back to v1alpha1 only if not found or kind not registered (e.g. server only has v1alpha1).
	if !kerrors.IsNotFound(err) {
		s := err.Error()
		if !strings.Contains(s, "could not find the requested resource") && !strings.Contains(s, "no kind is registered") && !strings.Contains(s, "unable to find") {
			return nil, fmt.Errorf("get APIExport %s: %w", apiExportName, err)
		}
	}

	var exportV1alpha1 kcpapiv1alpha1.APIExport
	if err := kcpClient.Get(ctx, client.ObjectKey{Name: apiExportName}, &exportV1alpha1); err != nil {
		return nil, fmt.Errorf("get APIExport %s (v1alpha1 fallback): %w", apiExportName, err)
	}
	var converted kcpapiv1alpha2.APIExport
	if err := kcpClient.Scheme().Convert(&exportV1alpha1, &converted, nil); err != nil {
		return nil, fmt.Errorf("convert APIExport v1alpha1 to v1alpha2: %w", err)
	}
	return &converted, nil
}

// getPolicyRulesFromAPIExport builds PolicyRules from the APIExport (v1alpha2): spec.Resources (full access), spec.PermissionClaims (verbs), and a static rule for apiexports/content.
func getPolicyRulesFromAPIExport(export *kcpapiv1alpha2.APIExport) ([]rbacv1.PolicyRule, error) {
	var rules []rbacv1.PolicyRule

	// Full access to exported resources (spec.resources).
	// Also grant get/update/patch on the status subresource so controllers can update .status (e.g. ContentConfiguration conditions).
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

	// permissionClaims: use Verbs; if empty use "*". identityHash is ignored.
	// Also grant get/update/patch on resource/status so controllers can update status when the claim allows write verbs.
	// Skip claims with empty resource name to avoid invalid PolicyRules.
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

	// Static rule so the SA can access the APIExport content (virtual workspace).
	if export.ObjectMeta.Name != "" {
		rules = append(rules, rbacv1.PolicyRule{
			APIGroups:     []string{"apis.kcp.io"},
			Resources:     []string{"apiexports/content"},
			ResourceNames: []string{export.ObjectMeta.Name},
			Verbs:         []string{"*"},
		})
	}

	// Allow get/list/watch APIExportEndpointSlice in the export workspace (so the provider can resolve the slice and its endpoints).
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{"apis.kcp.io"},
		Resources: []string{"apiexportendpointslices"},
		Verbs:     []string{"get", "list", "watch"},
	})

	// Allow listing APIBindings (required by operators that use informers for apis.kcp.io/v1alpha1.APIBinding).
	rules = append(rules, rbacv1.PolicyRule{
		APIGroups: []string{"apis.kcp.io"},
		Resources: []string{"apibindings"},
		Verbs:     []string{"get", "list", "watch"},
	})

	// Allow API discovery (server groups). Required for discovery and for informers (e.g. APIBinding).
	// Root discovery (/api, /apis) plus /clusters/* so KCP can evaluate discovery under workspace paths.
	rules = append(rules, rbacv1.PolicyRule{
		NonResourceURLs: []string{"/api", "/api/*", "/apis", "/apis/*", "/clusters/*"},
		Verbs:           []string{"get"},
	})

	return rules, nil
}

// hasUpdatePatchVerbs returns true if verbs include update or patch (or "*").
func hasUpdatePatchVerbs(verbs []string) bool {
	for _, v := range verbs {
		if v == "*" || v == "update" || v == "patch" {
			return true
		}
	}
	return false
}

// Predefined rule blocks that can be enabled via ServiceAccountPermissions flags (enableGetLogicalCluster, enableStoresAccess, etc.).
var (
	ruleBlockGetLogicalCluster = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"core.kcp.io"},
			Resources:     []string{"logicalclusters"},
			ResourceNames: []string{"cluster"},
			Verbs:         []string{"get"},
		},
	}
	ruleBlockStoresAccess = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"core.platform-mesh.io"},
			Resources: []string{"stores"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	ruleBlockInitTargetsAccess = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"initialization.kcp.io"},
			Resources: []string{"inittargets"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
)

// getExtraPolicyRulesFromFlags returns the concatenated rule blocks for each enabled flag.
func getExtraPolicyRulesFromFlags(flags *ExtraPolicyRulesFlags) []rbacv1.PolicyRule {
	if flags == nil {
		return nil
	}
	var out []rbacv1.PolicyRule
	if flags.EnableGetLogicalCluster != nil && *flags.EnableGetLogicalCluster {
		out = append(out, ruleBlockGetLogicalCluster...)
	}
	if flags.EnableStoresAccess != nil && *flags.EnableStoresAccess {
		out = append(out, ruleBlockStoresAccess...)
	}
	if flags.EnableInitTargetsAccess != nil && *flags.EnableInitTargetsAccess {
		out = append(out, ruleBlockInitTargetsAccess...)
	}
	return out
}

// ensureScopedProviderServiceAccountAndRBAC creates or updates a ServiceAccount, ClusterRole, ClusterRoleBinding, and a binding to system:kcp:workspace:access in the KCP workspace for the scoped provider flow.
// providerSuffix is used to name the SA/ClusterRole per provider (e.g. sanitized secret name) so each provider has its own identity; pass "" to use the default suffix (e.g. in tests).
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

	// Bind SA to KCP's pre-defined workspace access role so the workspace content authorizer allows the SA before local RBAC.
	// Without this, discovery (e.g. GET /api, /apis) can fail with "failed to get server groups: unknown" in some KCP setups.
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

// createTokenForSA creates a token for the ServiceAccount in the KCP workspace using TokenRequest.
// expirationSeconds: if > 0 use it; otherwise use defaultTokenExpirationSeconds.
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

// BuildHostURLForScoped returns hostPort + "/clusters/" + path for scoped kubeconfig server URL.
func BuildHostURLForScoped(hostPort, path string) (string, error) {
	return url.JoinPath(hostPort, "clusters", path)
}

// WriteScopedKubeconfigToSecret builds the scoped kubeconfig server URL, resolves the APIExport, ensures SA+RBAC in the KCP workspace,
// creates a token, and persists the kubeconfig into a Secret via k8sClient. hostURL must point at the export workspace
// (e.g. https://kcp/clusters/root:platform-mesh-system) so the SA can use APIExportEndpointSlices and export content there.
func WriteScopedKubeconfigToSecret(ctx context.Context, k8sClient client.Client, cfg *rest.Config, spec ProviderConnectionSpec, hostPort, secretNamespace string) error {
	if spec.APIExportName == "" {
		return fmt.Errorf("scoped kubeconfig requires APIExportName")
	}
	if spec.Path == "" {
		return fmt.Errorf("scoped kubeconfig requires Path (export workspace)")
	}

	hostURL, err := BuildHostURLForScoped(hostPort, spec.Path)
	if err != nil {
		return fmt.Errorf("build host URL for scoped kubeconfig: %w", err)
	}
	if secretNamespace == "" {
		secretNamespace = defaultScopedSecretNamespace
	}
	caData := cfg.TLSClientConfig.CAData
	if caData == nil {
		caData = []byte{}
	}

	// Resolve APIExport (v1alpha2, with v1alpha1 fallback).
	export, err := resolveAPIExport(ctx, cfg, spec.APIExportName, spec.Path)
	if err != nil {
		return errors.Wrap(err, "resolve APIExport")
	}

	rules, err := getPolicyRulesFromAPIExport(export)
	if err != nil {
		return errors.Wrap(err, "build RBAC from APIExport")
	}
	if extra := getExtraPolicyRulesFromFlags(spec.ExtraPolicyRuleBlocks); len(extra) > 0 {
		rules = append(rules, extra...)
	}

	// Create or update SA and RBAC in the export workspace (Path).
	kcpClient, err := newKCPClientWithRBAC(cfg, spec.Path)
	if err != nil {
		return errors.Wrap(err, "create KCP client for SA workspace")
	}
	providerSuffix := sanitizeSecretNameForRBAC(spec.Secret)
	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, kcpClient, rules, providerSuffix)
	if err != nil {
		return errors.Wrap(err, "ensure ServiceAccount and RBAC")
	}

	// Create token and build kubeconfig.
	token, err := createTokenForSA(ctx, cfg, spec.Path, defaultScopedSANamespace, saName, defaultTokenExpirationSeconds)
	if err != nil {
		return errors.Wrap(err, "create token for ServiceAccount")
	}
	kubeconfig := BuildScopedKubeconfig(hostURL, token, caData)
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errors.Wrap(err, "write kubeconfig")
	}

	// Persist secret.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Secret, Namespace: secretNamespace},
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

// BuildScopedKubeconfig builds a kubeconfig that uses the given token and CA for the cluster at hostURL.
func BuildScopedKubeconfig(hostURL string, token string, caData []byte) *clientcmdapi.Config {
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

// Admin SA prefix for kubeconfigAuth: serviceAccountAdmin. Each provider gets its own SA: platform-mesh-admin-<providerSuffix>.
const adminSAPrefix = "platform-mesh-admin-"

// ensureAdminServiceAccountAndRBACForProvider creates or updates a per-provider admin ServiceAccount and ClusterRole
// (wildcard plus optional get logicalclusters) in the KCP workspace, plus workspace access binding.
// providerSuffix is derived from the provider's secret name (e.g. sanitizeSecretNameForRBAC(secretName)) so each provider
// has its own identity. When enableGetLogicalCluster is true, the ClusterRole includes get on core.kcp.io logicalclusters.
func ensureAdminServiceAccountAndRBACForProvider(ctx context.Context, kcpClient client.Client, providerSuffix string, enableGetLogicalCluster bool) (saName string, err error) {
	if providerSuffix == "" {
		providerSuffix = scopedProviderRBACNameDefault
	}
	saNamespace := defaultScopedSANamespace
	saName = adminSAPrefix + providerSuffix
	crName := saName
	workspaceAccessCRBName := "platform-mesh-admin-workspace-access-" + providerSuffix

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

	crRules := []rbacv1.PolicyRule{
		{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}},
		{NonResourceURLs: []string{"*"}, Verbs: []string{"*"}},
	}
	if enableGetLogicalCluster {
		crRules = append(crRules, ruleBlockGetLogicalCluster...)
	}

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Rules:      crRules,
	}
	if err := kcpClient.Create(ctx, cr); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("create ClusterRole %s: %w", crName, err)
		}
		if err := kcpClient.Get(ctx, client.ObjectKey{Name: crName}, cr); err != nil {
			return "", err
		}
		cr.Rules = crRules
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
			{Kind: rbacv1.ServiceAccountKind, Namespace: saNamespace, Name: saName},
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
			{Kind: rbacv1.ServiceAccountKind, Namespace: saNamespace, Name: saName},
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("create or update ClusterRoleBinding %s: %w", workspaceAccessCRBName, err)
	}
	return saName, nil
}

// WriteAdminSAKubeconfigToSecret ensures the admin SA and RBAC exist in the KCP workspace, creates a token,
// and writes the kubeconfig to a Secret. Used for kubeconfigAuth: serviceAccountAdmin.
// The server URL is hostPort + "/clusters/" + workspacePath.
// When enableGetLogicalCluster is true, the admin ClusterRole includes get on core.kcp.io logicalclusters (for listener etc.).
func WriteAdminSAKubeconfigToSecret(ctx context.Context, k8sClient client.Client, cfg *rest.Config, workspacePath, secretName, hostPort, secretNamespace string, enableGetLogicalCluster bool) error {
	hostURL, err := BuildHostURLForScoped(hostPort, workspacePath)
	if err != nil {
		return fmt.Errorf("build host URL for admin SA kubeconfig: %w", err)
	}
	return writeAdminSAKubeconfigToSecretWithHostURL(ctx, k8sClient, cfg, workspacePath, secretName, hostURL, secretNamespace, enableGetLogicalCluster)
}

// WriteAdminSAKubeconfigToSecretWithServerURL is like WriteAdminSAKubeconfigToSecret but uses the given serverURL
// as the kubeconfig server (e.g. APIExport endpoint from APIExportEndpointSlice). Same endpoint as admin cert
// so discovery (e.g. apis.kcp.io) is consistent.
func WriteAdminSAKubeconfigToSecretWithServerURL(ctx context.Context, k8sClient client.Client, cfg *rest.Config, workspacePath, secretName, serverURL, secretNamespace string, enableGetLogicalCluster bool) error {
	return writeAdminSAKubeconfigToSecretWithHostURL(ctx, k8sClient, cfg, workspacePath, secretName, serverURL, secretNamespace, enableGetLogicalCluster)
}

func writeAdminSAKubeconfigToSecretWithHostURL(ctx context.Context, k8sClient client.Client, cfg *rest.Config, workspacePath, secretName, hostURL, secretNamespace string, enableGetLogicalCluster bool) error {
	if workspacePath == "" {
		return fmt.Errorf("admin SA kubeconfig requires workspace path")
	}
	if secretName == "" {
		return fmt.Errorf("admin SA kubeconfig requires secret name")
	}
	if hostURL == "" {
		return fmt.Errorf("admin SA kubeconfig requires host URL")
	}
	if secretNamespace == "" {
		secretNamespace = defaultScopedSecretNamespace
	}
	caData := cfg.TLSClientConfig.CAData
	if caData == nil {
		caData = []byte{}
	}

	// Admin SA is created in root (same level as admin cert) so the identity is root-level; kubeconfig server URL (hostURL) is unchanged per provider.
	rootClient, err := newKCPClientWithRBAC(cfg, adminSAWorkspacePath)
	if err != nil {
		return errors.Wrap(err, "create KCP client for admin SA workspace (root)")
	}
	providerSuffix := sanitizeSecretNameForRBAC(secretName)
	saName, err := ensureAdminServiceAccountAndRBACForProvider(ctx, rootClient, providerSuffix, enableGetLogicalCluster)
	if err != nil {
		return errors.Wrap(err, "ensure admin ServiceAccount and RBAC")
	}

	// When the kubeconfig targets a non-root workspace (e.g. root:platform-mesh-system), the admin SA lives in root
	// but requests go to that workspace; KCP rewrites the identity to system:cluster:<root's cluster ID> there.
	// Grant that identity access to apis.kcp.io and discovery in the provider workspace so the listener can list APIBindings etc.
	if workspacePath != "" && workspacePath != adminSAWorkspacePath {
		var rootLC kcpcorev1alpha.LogicalCluster
		if err := rootClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &rootLC); err != nil {
			return errors.Wrap(err, "get LogicalCluster cluster in root to resolve root cluster ID for provider workspace access")
		}
		rootClusterID := logicalcluster.From(&rootLC).String()
		if rootClusterID == "" {
			return errors.Wrap(fmt.Errorf("LogicalCluster cluster in root has no kcp.io/cluster annotation; required for provider workspace RBAC (system:cluster:<id>)"), "resolve root cluster ID")
		}
		providerClient, err := newKCPClientWithRBAC(cfg, workspacePath)
		if err != nil {
			return errors.Wrap(err, "create KCP client for provider workspace")
		}
		if err := EnsureProviderWorkspaceAccessForRootCluster(ctx, providerClient, rootClusterID); err != nil {
			return errors.Wrap(err, "ensure provider workspace access for root cluster (apis.kcp.io)")
		}
	}

	// When the gateway (or any admin SA with enableGetLogicalCluster) does get logicalclusters, KCP evaluates
	// the request at cluster scope (root:orgs) and presents the identity as system:cluster:<clusterID> where
	// clusterID is the logical cluster the kubeconfig is targeting (workspacePath), not root. So we must bind
	// the cluster ID of workspacePath in root:orgs, not root's cluster ID.
	if enableGetLogicalCluster {
		providerClient, err := newKCPClientWithRBAC(cfg, workspacePath)
		if err != nil {
			return errors.Wrap(err, "create KCP client for provider workspace (to resolve cluster ID)")
		}
		var lc kcpcorev1alpha.LogicalCluster
		if err := providerClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
			return errors.Wrap(err, "get LogicalCluster cluster in provider workspace to resolve cluster ID for root:orgs access")
		}
		// KCP uses the logical cluster name (kcp.io/cluster annotation) for system:cluster:<id>.
		clusterID := logicalcluster.From(&lc).String()
		if clusterID == "" {
			return errors.Wrap(fmt.Errorf("LogicalCluster cluster has no kcp.io/cluster annotation; required for root:orgs RBAC (system:cluster:<id>)"), "resolve cluster ID")
		}
		orgsClient, err := newKCPClientWithRBAC(cfg, "root:orgs")
		if err != nil {
			return errors.Wrap(err, "create KCP client for root:orgs")
		}
		if err := EnsureRootOrgsAccess(ctx, orgsClient, clusterID); err != nil {
			return errors.Wrap(err, "ensure root:orgs access for get logicalcluster (cluster-scope)")
		}
	}

	token, err := createTokenForSA(ctx, cfg, adminSAWorkspacePath, defaultScopedSANamespace, saName, defaultTokenExpirationSeconds)
	if err != nil {
		return errors.Wrap(err, "create token for admin ServiceAccount")
	}
	kubeconfig := BuildScopedKubeconfig(hostURL, token, caData)
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errors.Wrap(err, "write kubeconfig")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: secretNamespace},
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

// providerWorkspaceRootClusterCRBName is the ClusterRoleBinding in the provider workspace (e.g. root:platform-mesh-system)
// that binds system:cluster:<root's cluster ID> to the built-in cluster-admin ClusterRole. Used when the admin SA
// (which lives in root) connects to a non-root workspace: KCP rewrites the identity to that group.
const providerWorkspaceRootClusterCRBName = "platform-mesh-admin-root-cluster-access"

// rootOrgsRBACNames are the names used in root:orgs for the scoped provider (e.g. rebac) cross-workspace access.
// We bind system:cluster:<id> to workspace access and to cluster-admin (bootstrap policy) so no custom ClusterRole
// is required; KCP docs state cluster-admin is defined in system:admin and applies to every workspace including root:orgs.
const (
	rootOrgsWorkspaceAccessCRBName = "platform-mesh-scoped-provider-workspace-access"
	rootOrgsResourcesCRName        = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsResourcesCRBName       = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsClusterAdminCRBName    = "platform-mesh-scoped-provider-root-orgs-cluster-admin"
)

// EnsureProviderWorkspaceAccessForRootCluster ensures the root-origin identity (system:cluster:<rootClusterID>) has
// full access in the provider workspace by binding it to the built-in cluster-admin ClusterRole. Required when the
// admin SA lives in root but the kubeconfig targets a child workspace: KCP rewrites the identity to
// system:cluster:<root's cluster ID> in that workspace. KCP provides cluster-admin in each workspace (Kubernetes
// convention), so we don't create or maintain a custom ClusterRole.
func EnsureProviderWorkspaceAccessForRootCluster(ctx context.Context, providerClient client.Client, rootClusterID string) error {
	if rootClusterID == "" {
		return fmt.Errorf("rootClusterID is required for EnsureProviderWorkspaceAccessForRootCluster")
	}
	subject := rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "system:cluster:" + rootClusterID}
	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: providerWorkspaceRootClusterCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, providerClient, crb, func() error {
		crb.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"}
		crb.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// EnsureRootOrgsAccess creates or updates RBAC in the root:orgs workspace so that a scoped identity from another
// workspace (system:cluster:<clusterID>) can access root:orgs (workspace access + get logicalcluster "cluster" + stores).
// orgsClient must be a client configured for the root:orgs workspace.
func EnsureRootOrgsAccess(ctx context.Context, orgsClient client.Client, clusterID string) error {
	if clusterID == "" {
		return fmt.Errorf("clusterID is required for EnsureRootOrgsAccess")
	}
	subject := rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "system:cluster:" + clusterID}

	// 1. Workspace access binding
	workspaceAccessCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsWorkspaceAccessCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, workspaceAccessCRB, func() error {
		workspaceAccessCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: kcpWorkspaceAccessRoleName}
		workspaceAccessCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}

	// 2. ClusterRole for logicalcluster "cluster" and stores
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: rootOrgsResourcesCRName},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"core.kcp.io"}, Resources: []string{"logicalclusters"}, ResourceNames: []string{"cluster"}, Verbs: []string{"get"}},
			{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"stores"}, Verbs: []string{"get", "list", "watch"}},
		},
	}
	if err := orgsClient.Create(ctx, cr); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return err
		}
		if err := orgsClient.Get(ctx, client.ObjectKey{Name: rootOrgsResourcesCRName}, cr); err != nil {
			return err
		}
		cr.Rules = []rbacv1.PolicyRule{
			{APIGroups: []string{"core.kcp.io"}, Resources: []string{"logicalclusters"}, ResourceNames: []string{"cluster"}, Verbs: []string{"get"}},
			{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"stores"}, Verbs: []string{"get", "list", "watch"}},
		}
		if err := orgsClient.Update(ctx, cr); err != nil {
			return err
		}
	}

	// 3. Bind group to the resources ClusterRole (minimal: logicalclusters, stores)
	resourcesCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsResourcesCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, resourcesCRB, func() error {
		resourcesCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: rootOrgsResourcesCRName}
		resourcesCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}

	// 4. Bind group to built-in cluster-admin so all cluster-scope and resource access is granted without maintaining rules.
	// KCP Bootstrap Policy Authorizer defines cluster-admin in system:admin; it applies to every workspace including root:orgs.
	clusterAdminCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsClusterAdminCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, clusterAdminCRB, func() error {
		clusterAdminCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"}
		clusterAdminCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// ExtraPolicyRulesFlags are boolean flags that enable named rule blocks (mirrors ServiceAccountPermissions flags).
type ExtraPolicyRulesFlags struct {
	EnableGetLogicalCluster *bool
	EnableStoresAccess      *bool
	EnableInitTargetsAccess *bool
}

// ProviderConnectionSpec is the minimal spec needed for scoped kubeconfig (avoids importing api/v1alpha1 in tests).
// Same pattern as consumers: Path = export workspace (from context), APIExportName = which export (only name in config).
type ProviderConnectionSpec struct {
	Path                  string // KCP workspace path for the export; also where SA is created and hostURL points.
	Secret                string
	APIExportName         string
	ExtraPolicyRuleBlocks *ExtraPolicyRulesFlags // Optional: enable named rule blocks (from ServiceAccountPermissions flags).
}
