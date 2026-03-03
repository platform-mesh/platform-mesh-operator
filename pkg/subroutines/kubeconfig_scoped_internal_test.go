package subroutines

import (
	"context"
	"reflect"
	"testing"

	kcpapiv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

// Test_rbacFromAPIExport checks that rbacFromAPIExport builds the right PolicyRules from an APIExport.
// It verifies that: exported resources get full access plus status; permission claims get their verbs
// (and status when they have write verbs); apiexports/content is present for the export name; and the
// static rules exist (apiexportendpointslices, apibindings, and API discovery non-resource URLs).
func Test_rbacFromAPIExport(t *testing.T) {
	export := &kcpapiv1alpha2.APIExport{
		ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
		Spec: kcpapiv1alpha2.APIExportSpec{
			Resources: []kcpapiv1alpha2.ResourceSchema{
				{Group: "core.platform-mesh.io", Name: "accounts"},
				{Group: "ui.platform-mesh.io", Name: "contentconfigurations"},
			},
			PermissionClaims: []kcpapiv1alpha2.PermissionClaim{
				{GroupResource: kcpapiv1alpha2.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, Verbs: []string{"get", "list", "watch"}},
				{GroupResource: kcpapiv1alpha2.GroupResource{Group: "apis.kcp.io", Resource: "apibindings"}, Verbs: []string{"*"}},
			},
		},
	}

	rules, err := rbacFromAPIExport(export)
	require.NoError(t, err)
	require.NotEmpty(t, rules)

	// Expect rules for: 2 resources (* + status each), 2 permission claims (one with status), apiexports/content, apiexportendpointslices, apibindings, non-resource URLs
	assert.True(t, len(rules) >= 10, "expected at least 10 rules, got %d", len(rules))

	// Check exported resources get full access + status
	var foundAccounts, foundContentConfig bool
	for _, r := range rules {
		if len(r.APIGroups) == 1 && len(r.Resources) == 1 {
			if r.APIGroups[0] == "core.platform-mesh.io" && r.Resources[0] == "accounts" {
				foundAccounts = true
			}
			if r.APIGroups[0] == "ui.platform-mesh.io" && r.Resources[0] == "contentconfigurations" {
				foundContentConfig = true
			}
		}
	}
	assert.True(t, foundAccounts, "expected rule for accounts")
	assert.True(t, foundContentConfig, "expected rule for contentconfigurations")

	// Check apiexports/content for this export
	var foundExportContent bool
	for _, r := range rules {
		if len(r.Resources) > 0 && r.Resources[0] == "apiexports/content" && len(r.ResourceNames) > 0 && r.ResourceNames[0] == "core.platform-mesh.io" {
			foundExportContent = true
			break
		}
	}
	assert.True(t, foundExportContent, "expected rule for apiexports/content with ResourceNames core.platform-mesh.io")

	// Check static rules exist
	var foundSlices, foundBindings, foundDiscovery bool
	for _, r := range rules {
		if len(r.Resources) == 1 && r.Resources[0] == "apiexportendpointslices" {
			foundSlices = true
		}
		if len(r.Resources) == 1 && r.Resources[0] == "apibindings" {
			foundBindings = true
		}
		if len(r.NonResourceURLs) > 0 {
			foundDiscovery = true
		}
	}
	assert.True(t, foundSlices, "expected rule for apiexportendpointslices")
	assert.True(t, foundBindings, "expected rule for apibindings")
	assert.True(t, foundDiscovery, "expected rule for API discovery non-resource URLs")
}

// Test_rbacFromAPIExport_exactRules asserts the full rule set against an expected slice.
// Any change to RBAC logic (new rules, different verbs, different static URLs, etc.) will fail
// this test until the expected slice is updated.
func Test_rbacFromAPIExport_exactRules(t *testing.T) {
	export := &kcpapiv1alpha2.APIExport{
		ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
		Spec: kcpapiv1alpha2.APIExportSpec{
			Resources: []kcpapiv1alpha2.ResourceSchema{
				{Group: "core.platform-mesh.io", Name: "accounts"},
				{Group: "ui.platform-mesh.io", Name: "contentconfigurations"},
			},
			PermissionClaims: []kcpapiv1alpha2.PermissionClaim{
				{GroupResource: kcpapiv1alpha2.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, Verbs: []string{"get", "list", "watch"}},
				{GroupResource: kcpapiv1alpha2.GroupResource{Group: "apis.kcp.io", Resource: "apibindings"}, Verbs: []string{"*"}},
			},
		},
	}

	got, err := rbacFromAPIExport(export)
	require.NoError(t, err)

	// Expected rules in the same order as rbacFromAPIExport: exported resources (main + status),
	// permission claims (main + status only when claim has write verb), apiexports/content for
	// this export, then the three static rules (apiexportendpointslices, apibindings, discovery URLs).
	expected := []rbacv1.PolicyRule{
		// 2 resources × (main + status)
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts/status"}, Verbs: []string{"get", "update", "patch"}},
		{APIGroups: []string{"ui.platform-mesh.io"}, Resources: []string{"contentconfigurations"}, Verbs: []string{"*"}},
		{APIGroups: []string{"ui.platform-mesh.io"}, Resources: []string{"contentconfigurations/status"}, Verbs: []string{"get", "update", "patch"}},
		// 2 permission claims: workspaces (read-only → no status), apibindings (* → status)
		{APIGroups: []string{"tenancy.kcp.io"}, Resources: []string{"workspaces"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"apis.kcp.io"}, Resources: []string{"apibindings"}, Verbs: []string{"*"}},
		{APIGroups: []string{"apis.kcp.io"}, Resources: []string{"apibindings/status"}, Verbs: []string{"get", "update", "patch"}},
		// apiexports/content for this export
		{APIGroups: []string{"apis.kcp.io"}, Resources: []string{"apiexports/content"}, ResourceNames: []string{"core.platform-mesh.io"}, Verbs: []string{"*"}},
		// static
		{APIGroups: []string{"apis.kcp.io"}, Resources: []string{"apiexportendpointslices"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"apis.kcp.io"}, Resources: []string{"apibindings"}, Verbs: []string{"get", "list", "watch"}},
		{NonResourceURLs: []string{"/api", "/api/*", "/apis", "/apis/*", "/clusters/*"}, Verbs: []string{"get"}},
	}

	assert.Equal(t, expected, got, "RBAC rules must match exactly; any change here should be intentional and this test updated")
}

// Test_rbacFromAPIExport_edgeCases covers edge behaviour: when the export has no name we do not
// add apiexports/content; when a permission claim has empty verbs we default to "*"; and when a
// claim has a write verb (update/patch) we add a resource/status rule so controllers can update status.
func Test_rbacFromAPIExport_edgeCases(t *testing.T) {
	t.Run("empty export name skips apiexports/content rule", func(t *testing.T) {
		export := &kcpapiv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{Name: ""},
			Spec:       kcpapiv1alpha2.APIExportSpec{Resources: []kcpapiv1alpha2.ResourceSchema{{Group: "foo", Name: "bars"}}},
		}
		rules, err := rbacFromAPIExport(export)
		require.NoError(t, err)
		for _, r := range rules {
			assert.NotContains(t, r.Resources, "apiexports/content", "should not add apiexports/content when export name is empty")
		}
	})

	t.Run("permission claim with empty verbs gets star", func(t *testing.T) {
		export := &kcpapiv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{Name: "x"},
			Spec: kcpapiv1alpha2.APIExportSpec{
				PermissionClaims: []kcpapiv1alpha2.PermissionClaim{
					{GroupResource: kcpapiv1alpha2.GroupResource{Group: "g", Resource: "r"}, Verbs: nil},
				},
			},
		}
		rules, err := rbacFromAPIExport(export)
		require.NoError(t, err)
		var found bool
		for _, r := range rules {
			if len(r.APIGroups) == 1 && r.APIGroups[0] == "g" && len(r.Resources) == 1 && r.Resources[0] == "r" {
				assert.Contains(t, r.Verbs, "*", "empty verbs should default to *")
				found = true
				break
			}
		}
		assert.True(t, found, "expected rule for permission claim resource")
	})

	t.Run("permission claim with write verb adds status rule", func(t *testing.T) {
		export := &kcpapiv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{Name: "x"},
			Spec: kcpapiv1alpha2.APIExportSpec{
				PermissionClaims: []kcpapiv1alpha2.PermissionClaim{
					{GroupResource: kcpapiv1alpha2.GroupResource{Group: "g", Resource: "r"}, Verbs: []string{"get", "update"}},
				},
			},
		}
		rules, err := rbacFromAPIExport(export)
		require.NoError(t, err)
		var foundStatus bool
		for _, r := range rules {
			if len(r.Resources) == 1 && r.Resources[0] == "r/status" {
				foundStatus = true
				break
			}
		}
		assert.True(t, foundStatus, "write-verb claim should get resource/status rule")
	})
}

// Test_buildKCPConfigForPath checks that the returned config is a copy of the input with Host set
// to scheme+host + "/clusters/" + workspacePath, so the client talks to the right KCP workspace.
func Test_buildKCPConfigForPath(t *testing.T) {
	cfg := &rest.Config{Host: "https://localhost:8443"}
	path := "root:platform-mesh-system"
	out := buildKCPConfigForPath(cfg, path)
	require.NotSame(t, cfg, out, "should return a copy of the config")
	assert.Equal(t, "https://localhost:8443/clusters/root:platform-mesh-system", out.Host)
}

// Test_sanitizeProviderKey ensures that provider keys with underscores or spaces are turned into
// valid Kubernetes resource names (e.g. for the ServiceAccount, ClusterRole, and ClusterRoleBinding
// we create in the KCP workspace). Underscores and spaces are replaced with dashes.
func Test_sanitizeProviderKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"my-provider", "my-provider"},
		{"my_provider", "my-provider"},
		{"my provider", "my-provider"},
		{"a_b_c", "a-b-c"},
		{"  x  ", "--x--"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := sanitizeProviderKey(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_ensureServiceAccountAndRBAC_validatesRBACResourcesCreated checks that ensureServiceAccountAndRBAC
// creates the expected RBAC resources in the KCP workspace: a ServiceAccount, a ClusterRole (with the
// given policy rules), and two ClusterRoleBindings—one binding the SA to our ClusterRole, and one
// binding the SA to system:kcp:workspace:access for workspace content access. We mock the client so
// CreateOrUpdate sees NotFound on Get and thus performs Create for both CRBs; we also assert that the
// ClusterRole receives exactly the rules we pass in (no mutation or drop).
func Test_ensureServiceAccountAndRBAC_validatesRBACResourcesCreated(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	providerKey := "test-provider"
	saNamespace := "default"
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	// Create(ServiceAccount)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Namespace == saNamespace && sa.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()

	// Create(ClusterRole) — assert the role gets exactly the rules we pass in (no mutation or drop)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-test-provider" && reflect.DeepEqual(cr.Rules, rules)
	}), mock.Anything).Return(nil).Once()

	// CreateOrUpdate(provider CRB): Get returns NotFound so Create is used
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "platform-mesh-provider-test-provider")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-test-provider" &&
			crb.RoleRef.Name == "platform-mesh-provider-test-provider" &&
			crb.RoleRef.Kind == "ClusterRole" &&
			len(crb.Subjects) == 1 && crb.Subjects[0].Kind == "ServiceAccount" && crb.Subjects[0].Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()

	// CreateOrUpdate(workspace-access CRB): Get returns NotFound so Create is used
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "platform-mesh-workspace-access-test-provider")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-test-provider" &&
			crb.RoleRef.Name == "system:kcp:workspace:access" &&
			len(crb.Subjects) == 1 && crb.Subjects[0].Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureServiceAccountAndRBAC(ctx, mockClient, providerKey, saNamespace, rules)
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-test-provider", saName)

	mockClient.AssertExpectations(t)
}

// Test_ensureServiceAccountAndRBAC_whenServiceAccountAlreadyExists checks that when the ServiceAccount
// already exists (Create returns AlreadyExists), we continue and still create the ClusterRole and both
// ClusterRoleBindings. This is the typical case when reconciling again after a previous successful run.
func Test_ensureServiceAccountAndRBAC_whenServiceAccountAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	providerKey := "test-provider"
	saNamespace := "default"
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	// Create(SA) → AlreadyExists; we continue
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(apierrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "serviceaccounts"}, "platform-mesh-provider-test-provider")).Once()

	// Create(ClusterRole) and both CRBs same as happy path
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-test-provider" && reflect.DeepEqual(cr.Rules, rules)
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-test-provider"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureServiceAccountAndRBAC(ctx, mockClient, providerKey, saNamespace, rules)
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-test-provider", saName)
	mockClient.AssertExpectations(t)
}

// Test_ensureServiceAccountAndRBAC_whenClusterRoleAlreadyExists checks that when the ClusterRole
// already exists we do Get then Update with the new rules (e.g. after an APIExport change). The
// ServiceAccount and both ClusterRoleBindings are still created/updated as usual.
func Test_ensureServiceAccountAndRBAC_whenClusterRoleAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	providerKey := "test-provider"
	saNamespace := "default"
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()

	// Create(CR) → AlreadyExists; then Get(CR) and Update(CR)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(apierrors.NewAlreadyExists(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterroles"}, "platform-mesh-provider-test-provider")).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-test-provider"}, mock.AnythingOfType("*v1.ClusterRole")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			obj.(*rbacv1.ClusterRole).Rules = nil // simulate existing CR with old rules
			return nil
		}).Once()
	mockClient.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-test-provider" && reflect.DeepEqual(cr.Rules, rules)
	})).Return(nil).Once()

	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-test-provider"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-test-provider"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-test-provider"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureServiceAccountAndRBAC(ctx, mockClient, providerKey, saNamespace, rules)
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-test-provider", saName)
	mockClient.AssertExpectations(t)
}

// Test_ensureServiceAccountAndRBAC_defaultsNamespaceWhenEmpty checks that when saNamespace is ""
// we use the default namespace ("default") for the ServiceAccount and in the ClusterRoleBinding subjects.
func Test_ensureServiceAccountAndRBAC_defaultsNamespaceWhenEmpty(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	providerKey := "p"
	saNamespace := ""
	rules := []rbacv1.PolicyRule{}

	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Namespace == "default" && sa.Name == "platform-mesh-provider-p"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-p"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-p"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-p" && len(crb.Subjects) == 1 && crb.Subjects[0].Namespace == "default"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-p"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-p" && len(crb.Subjects) == 1 && crb.Subjects[0].Namespace == "default"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureServiceAccountAndRBAC(ctx, mockClient, providerKey, saNamespace, rules)
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-p", saName)
	mockClient.AssertExpectations(t)
}
