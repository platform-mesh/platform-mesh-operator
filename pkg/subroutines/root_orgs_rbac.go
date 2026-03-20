package subroutines

import (
	"context"
	"fmt"
	"net/url"

	kcpcorev1alpha "github.com/kcp-dev/kcp/sdk/apis/core/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// kcpWorkspaceAccessRoleName is the pre-defined KCP ClusterRole that grants workspace content access.
	kcpWorkspaceAccessRoleName = "system:kcp:workspace:access"

	// rootOrgsRBACNames for EnsureRootOrgsAccess (system:cluster:<id> bindings in root:orgs).
	rootOrgsWorkspaceAccessCRBName = "platform-mesh-scoped-provider-workspace-access"
	rootOrgsResourcesCRName        = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsResourcesCRBName       = "platform-mesh-scoped-provider-root-orgs-resources"
	rootOrgsClusterAdminCRBName    = "platform-mesh-scoped-provider-root-orgs-cluster-admin"
)

// providerWorkspacePath is the KCP workspace where providers (rebac, gateway, etc.) connect from.
const providerWorkspacePath = "root:platform-mesh-system"

// EnsureRootOrgsAccess creates or updates RBAC in the root:orgs workspace so that a scoped identity
// from another workspace (system:cluster:<clusterID>) can access root:orgs (workspace access +
// get logicalcluster "cluster" + stores + cluster-admin).
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

	// 3. Bind group to the resources ClusterRole
	resourcesCRB := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: rootOrgsResourcesCRBName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, orgsClient, resourcesCRB, func() error {
		resourcesCRB.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: rootOrgsResourcesCRName}
		resourcesCRB.Subjects = []rbacv1.Subject{subject}
		return nil
	}); err != nil {
		return err
	}

	// 4. Bind group to built-in cluster-admin (full access in root:orgs)
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

// newKCPClientWithRBAC returns a controller-runtime client for the given workspace path with rbacv1 and LogicalCluster in the scheme.
func newKCPClientWithRBAC(cfg *rest.Config, workspacePath string) (client.Client, error) {
	config := rest.CopyConfig(cfg)
	config.QPS = 1000.0
	config.Burst = 2000.0
	// Append /clusters/<path> to host (same pattern as Helper.NewKcpClient)
	u, err := url.Parse(cfg.Host)
	if err != nil {
		config.Host = cfg.Host + "/clusters/" + workspacePath
	} else {
		config.Host = u.Scheme + "://" + u.Host + "/clusters/" + workspacePath
	}
	scheme := newSchemeWithRBACAndLogicalCluster()
	cl, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create KCP client for path %s: %w", workspacePath, err)
	}
	return cl, nil
}

func newSchemeWithRBACAndLogicalCluster() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(kcpcorev1alpha.AddToScheme(scheme))
	return scheme
}

// EnsureRootOrgsAccessForProviderWorkspace ensures RBAC in root:orgs for the identity
// system:cluster:<clusterID> where clusterID is from the LogicalCluster "cluster" in root:platform-mesh-system.
// This allows providers (rebac, gateway, iam-service, etc.) that connect from root:platform-mesh-system
// to access root:orgs (e.g. for clustercache, Store reads, list accounts).
// Best-effort: logs warning on failure so provider secret creation can continue.
func EnsureRootOrgsAccessForProviderWorkspace(ctx context.Context, cfg *rest.Config) error {
	sourceClient, err := newKCPClientWithRBAC(cfg, providerWorkspacePath)
	if err != nil {
		return fmt.Errorf("create KCP client for %s: %w", providerWorkspacePath, err)
	}
	var lc kcpcorev1alpha.LogicalCluster
	if err := sourceClient.Get(ctx, types.NamespacedName{Name: "cluster"}, &lc); err != nil {
		return fmt.Errorf("get LogicalCluster cluster in %s: %w", providerWorkspacePath, err)
	}
	clusterID := logicalcluster.From(&lc).String()
	if clusterID == "" {
		return fmt.Errorf("LogicalCluster cluster in %s has no kcp.io/cluster annotation; required for root:orgs RBAC (system:cluster:<id>)", providerWorkspacePath)
	}
	orgsClient, err := newKCPClientWithRBAC(cfg, "root:orgs")
	if err != nil {
		return fmt.Errorf("create KCP client for root:orgs: %w", err)
	}
	return EnsureRootOrgsAccess(ctx, orgsClient, clusterID)
}
