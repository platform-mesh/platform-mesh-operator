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
	defaultScopedSANamespace      = "default"
	defaultScopedSecretNamespace  = "platform-mesh-system"
	secondsPerDay                 = 86400
	defaultTokenExpirationSeconds = 7 * secondsPerDay
	scopedClusterRolePrefix       = "platform-mesh-provider-"
	scopedSAPrefix                = "platform-mesh-provider-"
	kcpWorkspaceAccessRoleName    = "system:kcp:workspace:access"
	scopedProviderRBACNameDefault = "scoped"
	maxRBACNameSuffixLength       = 200
	adminSAWorkspacePath          = "root"
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
	schemeHost, err := URLSchemeHost(cfg.Host)
	if err != nil {
		schemeHost = cfg.Host
	}
	out.Host = schemeHost + "/clusters/" + workspacePath
	return out
}

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

var (
	ruleBlockGetLogicalCluster = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"core.kcp.io"},
			Resources:     []string{"logicalclusters"},
			ResourceNames: []string{"cluster"},
			Verbs:         []string{"get"},
		},
	}
)

func getExtraPolicyRulesFromFlags(flags *ExtraPolicyRulesFlags) []rbacv1.PolicyRule {
	if flags == nil {
		return nil
	}
	var out []rbacv1.PolicyRule
	if flags.EnableGetLogicalCluster != nil && *flags.EnableGetLogicalCluster {
		out = append(out, ruleBlockGetLogicalCluster...)
	}
	return out
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

func WriteScopedKubeconfigToSecret(ctx context.Context, k8sClient client.Client, cfg *rest.Config, spec ProviderConnectionSpec, hostPort, secretNamespace string) error {
	if spec.APIExportName == "" {
		return fmt.Errorf("scoped kubeconfig requires APIExportName")
	}
	if spec.Path == "" {
		return fmt.Errorf("scoped kubeconfig requires Path (export workspace)")
	}

	hostURL, err := url.JoinPath(hostPort, "clusters", spec.Path)
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

	kcpClient, err := newKCPClientWithRBAC(cfg, spec.Path)
	if err != nil {
		return errors.Wrap(err, "create KCP client for SA workspace")
	}
	providerSuffix := sanitizeSecretNameForRBAC(spec.Secret)
	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, kcpClient, rules, providerSuffix)
	if err != nil {
		return errors.Wrap(err, "ensure ServiceAccount and RBAC")
	}

	token, err := createTokenForSA(ctx, cfg, spec.Path, defaultScopedSANamespace, saName, defaultTokenExpirationSeconds)
	if err != nil {
		return errors.Wrap(err, "create token for ServiceAccount")
	}
	kubeconfig := BuildScopedKubeconfig(hostURL, token, caData)
	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return errors.Wrap(err, "write kubeconfig")
	}

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

const adminSAPrefix = "platform-mesh-admin-"

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

func writeAdminSAKubeconfigToSecretWithServerURL(ctx context.Context, k8sClient client.Client, cfg *rest.Config, workspacePath, secretName, serverURL, secretNamespace string, enableGetLogicalCluster bool) error {
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

	rootClient, err := newKCPClientWithRBAC(cfg, adminSAWorkspacePath)
	if err != nil {
		return errors.Wrap(err, "create KCP client for admin SA workspace (root)")
	}
	providerSuffix := sanitizeSecretNameForRBAC(secretName)
	saName, err := ensureAdminServiceAccountAndRBACForProvider(ctx, rootClient, providerSuffix, enableGetLogicalCluster)
	if err != nil {
		return errors.Wrap(err, "ensure admin ServiceAccount and RBAC")
	}

	if workspacePath != "" && workspacePath != adminSAWorkspacePath {
		// Cross-workspace calls from root identities are evaluated as system:cluster:<rootClusterID>.
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
		if err := ensureWorkspaceAccessForServiceAccount(ctx, providerClient, defaultScopedSANamespace, saName, "admin-"+providerSuffix, ""); err != nil {
			return errors.Wrap(err, "ensure provider workspace access for admin ServiceAccount identity")
		}
	}

	needsRootOrgsBindings := enableGetLogicalCluster
	if needsRootOrgsBindings {
		providerClient, err := newKCPClientWithRBAC(cfg, workspacePath)
		if err != nil {
			return errors.Wrap(err, "create KCP client for provider workspace (to resolve cluster ID)")
		}
		var lc kcpcorev1alpha.LogicalCluster
		if err := providerClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
			return errors.Wrap(err, "get LogicalCluster cluster in provider workspace to resolve cluster ID for root:orgs access")
		}
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
		if err := ensureRootOrgsAccessForServiceAccount(ctx, orgsClient, defaultScopedSANamespace, saName, "admin-"+providerSuffix); err != nil {
			return errors.Wrap(err, "ensure root:orgs access for admin ServiceAccount identity")
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

const providerWorkspaceRootClusterCRBName = "platform-mesh-admin-root-cluster-access"

const (
	rootOrgsWorkspaceAccessCRBName = "platform-mesh-scoped-provider-workspace-access"
	rootOrgsResourcesCRName        = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsResourcesCRBName       = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsClusterAdminCRBName    = "platform-mesh-scoped-provider-root-orgs-cluster-admin"
)

func ensureRootOrgsAccessForServiceAccount(ctx context.Context, orgsClient client.Client, saNamespace, saName, suffix string) error {
	if saNamespace == "" || saName == "" {
		return fmt.Errorf("service account namespace and name are required")
	}
	if suffix == "" {
		suffix = "default"
	}
	subject := rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Namespace: saNamespace, Name: saName}
	// Some checks use concrete username instead of ServiceAccount subject.
	userSubject := rbacv1.Subject{Kind: rbacv1.UserKind, Name: fmt.Sprintf("system:serviceaccount:%s:%s", saNamespace, saName)}

	workspaceAccessCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-scoped-provider-workspace-access-sa-" + suffix}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, workspaceAccessCRB, func() error {
		workspaceAccessCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: kcpWorkspaceAccessRoleName}
		workspaceAccessCRB.Subjects = []rbacv1.Subject{subject, userSubject}
		return nil
	}); err != nil {
		return err
	}

	clusterAdminCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-scoped-provider-root-orgs-cluster-admin-sa-" + suffix}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, clusterAdminCRB, func() error {
		clusterAdminCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"}
		clusterAdminCRB.Subjects = []rbacv1.Subject{subject, userSubject}
		return nil
	}); err != nil {
		return err
	}

	resourcesCRName := "platform-mesh-scoped-provider-root-orgs-resources-sa-" + suffix
	resourcesCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: resourcesCRName},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"core.kcp.io"}, Resources: []string{"logicalclusters"}, ResourceNames: []string{"cluster"}, Verbs: []string{"get"}},
			{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"stores"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"tenancy.kcp.io"}, Resources: []string{"workspacetypes"}, Verbs: []string{"get", "list", "watch"}},
		},
	}
	if err := orgsClient.Create(ctx, resourcesCR); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return err
		}
		if err := orgsClient.Get(ctx, client.ObjectKey{Name: resourcesCRName}, resourcesCR); err != nil {
			return err
		}
		resourcesCR.Rules = []rbacv1.PolicyRule{
			{APIGroups: []string{"core.kcp.io"}, Resources: []string{"logicalclusters"}, ResourceNames: []string{"cluster"}, Verbs: []string{"get"}},
			{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"stores"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"tenancy.kcp.io"}, Resources: []string{"workspacetypes"}, Verbs: []string{"get", "list", "watch"}},
		}
		if err := orgsClient.Update(ctx, resourcesCR); err != nil {
			return err
		}
	}

	resourcesCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-scoped-provider-root-orgs-resources-sa-" + suffix}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, resourcesCRB, func() error {
		resourcesCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: resourcesCRName}
		resourcesCRB.Subjects = []rbacv1.Subject{subject, userSubject}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func ensureWorkspaceAccessForServiceAccount(ctx context.Context, workspaceClient client.Client, saNamespace, saName, suffix, clusterID string) error {
	if saNamespace == "" || saName == "" {
		return fmt.Errorf("service account namespace and name are required")
	}
	if suffix == "" {
		suffix = "default"
	}
	subject := rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Namespace: saNamespace, Name: saName}
	userSubject := rbacv1.Subject{Kind: rbacv1.UserKind, Name: fmt.Sprintf("system:serviceaccount:%s:%s", saNamespace, saName)}
	subjects := []rbacv1.Subject{subject, userSubject}
	if clusterID != "" {
		// Include rewritten cluster identity for cross-workspace authorization.
		subjects = append(subjects, rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "system:cluster:" + clusterID})
	}

	adminCRName := "platform-mesh-admin-cluster-role-sa-" + suffix
	adminCR := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: adminCRName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, workspaceClient, adminCR, func() error {
		adminCR.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				NonResourceURLs: []string{"*"},
				Verbs:           []string{"*"},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	workspaceAccessCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-admin-workspace-access-sa-" + suffix}}
	if _, err := controllerutil.CreateOrUpdate(ctx, workspaceClient, workspaceAccessCRB, func() error {
		workspaceAccessCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: kcpWorkspaceAccessRoleName}
		workspaceAccessCRB.Subjects = subjects
		return nil
	}); err != nil {
		return err
	}

	clusterAdminCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "platform-mesh-admin-cluster-role-sa-" + suffix}}
	if _, err := controllerutil.CreateOrUpdate(ctx, workspaceClient, clusterAdminCRB, func() error {
		clusterAdminCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: adminCRName}
		clusterAdminCRB.Subjects = subjects
		return nil
	}); err != nil {
		return err
	}
	return nil
}

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

func EnsureRootOrgsAccess(ctx context.Context, orgsClient client.Client, clusterID string) error {
	if clusterID == "" {
		return fmt.Errorf("clusterID is required for EnsureRootOrgsAccess")
	}
	subject := rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "system:cluster:" + clusterID}

	workspaceAccessCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsWorkspaceAccessCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, workspaceAccessCRB, func() error {
		workspaceAccessCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: kcpWorkspaceAccessRoleName}
		workspaceAccessCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: rootOrgsResourcesCRName},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"core.kcp.io"}, Resources: []string{"logicalclusters"}, ResourceNames: []string{"cluster"}, Verbs: []string{"get"}},
			{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"stores"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"tenancy.kcp.io"}, Resources: []string{"workspacetypes"}, Verbs: []string{"get", "list", "watch"}},
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
			{APIGroups: []string{"tenancy.kcp.io"}, Resources: []string{"workspacetypes"}, Verbs: []string{"get", "list", "watch"}},
		}
		if err := orgsClient.Update(ctx, cr); err != nil {
			return err
		}
	}

	resourcesCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsResourcesCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, resourcesCRB, func() error {
		resourcesCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: rootOrgsResourcesCRName}
		resourcesCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}

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

type ExtraPolicyRulesFlags struct {
	EnableGetLogicalCluster *bool
}

type ProviderConnectionSpec struct {
	Path                  string
	Secret                string
	APIExportName         string
	ExtraPolicyRuleBlocks *ExtraPolicyRulesFlags
}
