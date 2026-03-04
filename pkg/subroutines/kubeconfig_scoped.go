package subroutines

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpapiv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	"github.com/platform-mesh/golang-commons/errors"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// scopedProviderRBACName is the fixed name suffix for the single SA/ClusterRole/CRB set used by the scoped provider flow.
	scopedProviderRBACName = "scoped"
)

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

// newKCPClientWithRBAC returns a controller-runtime client that can talk to the KCP workspace and has APIExport (v1alpha1 + v1alpha2), core/v1, and rbac/v1 in the scheme.
func newKCPClientWithRBAC(cfg *rest.Config, workspacePath string) (client.Client, error) {
	config := buildKCPConfigForPath(cfg, workspacePath)
	config.QPS = 1000.0
	config.Burst = 2000.0
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha2.AddToScheme(scheme))
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

// ensureScopedProviderServiceAccountAndRBAC creates or updates a ServiceAccount, ClusterRole, ClusterRoleBinding, and a binding to system:kcp:workspace:access in the KCP workspace for the scoped provider flow.
func ensureScopedProviderServiceAccountAndRBAC(ctx context.Context, kcpClient client.Client, policyRules []rbacv1.PolicyRule) (saName string, err error) {
	saName = scopedSAPrefix + scopedProviderRBACName
	crName := scopedClusterRolePrefix + scopedProviderRBACName
	workspaceAccessCRBName := "platform-mesh-workspace-access-" + scopedProviderRBACName
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

	// Create or update SA and RBAC in the export workspace (Path).
	kcpClient, err := newKCPClientWithRBAC(cfg, spec.Path)
	if err != nil {
		return errors.Wrap(err, "create KCP client for SA workspace")
	}
	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, kcpClient, rules)
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

// ProviderConnectionSpec is the minimal spec needed for scoped kubeconfig (avoids importing api/v1alpha1 in tests).
// Same pattern as consumers: Path = export workspace (from context), APIExportName = which export (only name in config).
type ProviderConnectionSpec struct {
	Path          string // KCP workspace path for the export; also where SA is created and hostURL points.
	Secret        string
	APIExportName string
}
