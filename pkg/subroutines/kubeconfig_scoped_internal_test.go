package subroutines

import (
	"context"
	"reflect"
	"testing"

	kcpapiv1alpha1 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcpapiv1alpha2 "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

// Test_getPolicyRulesFromAPIExport checks that getPolicyRulesFromAPIExport builds the right PolicyRules from an APIExport.
// It verifies that: exported resources get full access plus status; permission claims get their verbs
// (and status when they have write verbs); apiexports/content is present for the export name; and the
// static rules exist (apiexportendpointslices, apibindings, and API discovery non-resource URLs).
func Test_getPolicyRulesFromAPIExport(t *testing.T) {
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

	rules, err := getPolicyRulesFromAPIExport(export)
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

// Test_getPolicyRulesFromAPIExport_exactRules asserts the full rule set against an expected slice.
// Any change to RBAC logic (new rules, different verbs, different static URLs, etc.) will fail
// this test until the expected slice is updated.
func Test_getPolicyRulesFromAPIExport_exactRules(t *testing.T) {
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

	got, err := getPolicyRulesFromAPIExport(export)
	require.NoError(t, err)

	// Expected rules in the same order as getPolicyRulesFromAPIExport: exported resources (main + status),
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

// Test_getPolicyRulesFromAPIExport_edgeCases covers edge behaviour: when the export has no name we do not
// add apiexports/content; when a permission claim has empty verbs we default to "*"; and when a
// claim has a write verb (update/patch) we add a resource/status rule so controllers can update status.
func Test_getPolicyRulesFromAPIExport_edgeCases(t *testing.T) {
	t.Run("empty export name skips apiexports/content rule", func(t *testing.T) {
		export := &kcpapiv1alpha2.APIExport{
			ObjectMeta: metav1.ObjectMeta{Name: ""},
			Spec:       kcpapiv1alpha2.APIExportSpec{Resources: []kcpapiv1alpha2.ResourceSchema{{Group: "foo", Name: "bars"}}},
		}
		rules, err := getPolicyRulesFromAPIExport(export)
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
		rules, err := getPolicyRulesFromAPIExport(export)
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
		rules, err := getPolicyRulesFromAPIExport(export)
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

// Test_resolveAPIExport_fallbackV1alpha1Conversion verifies that when we have a v1alpha1 APIExport
// (e.g. from helm-charts or a server that only serves v1alpha1), the scheme conversion to v1alpha2
// produces an export that getPolicyRulesFromAPIExport can use. This is the same conversion used in resolveAPIExport's fallback path.
func Test_resolveAPIExport_fallbackV1alpha1Conversion(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha1.AddToScheme(scheme))
	utilruntime.Must(kcpapiv1alpha2.AddToScheme(scheme))

	// v1alpha1 shape: LatestResourceSchemas (schema names), PermissionClaims with All
	in := &kcpapiv1alpha1.APIExport{
		ObjectMeta: metav1.ObjectMeta{Name: "core.platform-mesh.io"},
		Spec: kcpapiv1alpha1.APIExportSpec{
			LatestResourceSchemas: []string{
				"v1.accounts.core.platform-mesh.io",
				"v1.contentconfigurations.ui.platform-mesh.io",
			},
			PermissionClaims: []kcpapiv1alpha1.PermissionClaim{
				{GroupResource: kcpapiv1alpha1.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, All: false}, // read-only
				{GroupResource: kcpapiv1alpha1.GroupResource{Group: "apis.kcp.io", Resource: "apibindings"}, All: true},
			},
		},
	}

	var out kcpapiv1alpha2.APIExport
	err := scheme.Convert(in, &out, nil)
	require.NoError(t, err)

	// Converted export should have Resources (group/name) and PermissionClaims with Verbs
	assert.Len(t, out.Spec.Resources, 2, "expected 2 resources from LatestResourceSchemas")
	assert.Len(t, out.Spec.PermissionClaims, 2, "expected 2 permission claims")
	rules, err := getPolicyRulesFromAPIExport(&out)
	require.NoError(t, err)
	require.NotEmpty(t, rules)

	// Sanity: we get rules for the exported resources and claims
	var foundAccounts, foundContentConfig, foundWorkspaces, foundAPIBindings bool
	for _, r := range rules {
		if len(r.APIGroups) == 1 && len(r.Resources) == 1 {
			if r.APIGroups[0] == "core.platform-mesh.io" && r.Resources[0] == "accounts" {
				foundAccounts = true
			}
			if r.APIGroups[0] == "ui.platform-mesh.io" && r.Resources[0] == "contentconfigurations" {
				foundContentConfig = true
			}
			if r.APIGroups[0] == "tenancy.kcp.io" && r.Resources[0] == "workspaces" {
				foundWorkspaces = true
			}
			if r.APIGroups[0] == "apis.kcp.io" && r.Resources[0] == "apibindings" {
				foundAPIBindings = true
			}
		}
	}
	assert.True(t, foundAccounts, "expected rule for accounts from converted export")
	assert.True(t, foundContentConfig, "expected rule for contentconfigurations from converted export")
	assert.True(t, foundWorkspaces, "expected rule for workspaces permission claim")
	assert.True(t, foundAPIBindings, "expected rule for apibindings permission claim")
}

// Test_ensureScopedProviderServiceAccountAndRBAC_validatesRBACResourcesCreated checks that ensureScopedProviderServiceAccountAndRBAC
// creates the expected RBAC resources in the KCP workspace: a ServiceAccount, a ClusterRole (with the
// given policy rules), and two ClusterRoleBindings—one binding the SA to our ClusterRole, and one
// binding the SA to system:kcp:workspace:access for workspace content access. We mock the client so
// CreateOrUpdate sees NotFound on Get and thus performs Create for both CRBs; we also assert that the
// ClusterRole receives exactly the rules we pass in (no mutation or drop).
func Test_ensureScopedProviderServiceAccountAndRBAC_validatesRBACResourcesCreated(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	// Create(ServiceAccount)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Namespace == "default" && sa.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()

	// Create(ClusterRole) — assert the role gets exactly the rules we pass in (no mutation or drop)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-scoped" && reflect.DeepEqual(cr.Rules, rules)
	}), mock.Anything).Return(nil).Once()

	// CreateOrUpdate(provider CRB): Get returns NotFound so Create is used
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "platform-mesh-provider-scoped")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-scoped" &&
			crb.RoleRef.Name == "platform-mesh-provider-scoped" &&
			crb.RoleRef.Kind == "ClusterRole" &&
			len(crb.Subjects) == 1 && crb.Subjects[0].Kind == "ServiceAccount" && crb.Subjects[0].Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()

	// CreateOrUpdate(workspace-access CRB): Get returns NotFound so Create is used
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "platform-mesh-workspace-access-scoped")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-scoped" &&
			crb.RoleRef.Name == "system:kcp:workspace:access" &&
			len(crb.Subjects) == 1 && crb.Subjects[0].Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, mockClient, rules, "scoped")
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-scoped", saName)

	mockClient.AssertExpectations(t)
}

// Test_ensureScopedProviderServiceAccountAndRBAC_whenServiceAccountAlreadyExists checks that when the ServiceAccount
// already exists (Create returns AlreadyExists), we continue and still create the ClusterRole and both
// ClusterRoleBindings. This is the typical case when reconciling again after a previous successful run.
func Test_ensureScopedProviderServiceAccountAndRBAC_whenServiceAccountAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	// Create(SA) → AlreadyExists; we continue
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(apierrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "serviceaccounts"}, "platform-mesh-provider-scoped")).Once()

	// Create(ClusterRole) and both CRBs same as happy path
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-scoped" && reflect.DeepEqual(cr.Rules, rules)
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-scoped"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, mockClient, rules, "scoped")
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-scoped", saName)
	mockClient.AssertExpectations(t)
}

// Test_ensureScopedProviderServiceAccountAndRBAC_whenClusterRoleAlreadyExists checks that when the ClusterRole
// already exists we do Get then Update with the new rules (e.g. after an APIExport change). The
// ServiceAccount and both ClusterRoleBindings are still created/updated as usual.
func Test_ensureScopedProviderServiceAccountAndRBAC_whenClusterRoleAlreadyExists(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"core.platform-mesh.io"}, Resources: []string{"accounts"}, Verbs: []string{"*"}},
	}

	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()

	// Create(CR) → AlreadyExists; then Get(CR) and Update(CR)
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(apierrors.NewAlreadyExists(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterroles"}, "platform-mesh-provider-scoped")).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-scoped"}, mock.AnythingOfType("*v1.ClusterRole")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			obj.(*rbacv1.ClusterRole).Rules = nil // simulate existing CR with old rules
			return nil
		}).Once()
	mockClient.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-scoped" && reflect.DeepEqual(cr.Rules, rules)
	})).Return(nil).Once()

	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: rbacv1.GroupName, Resource: "clusterrolebindings"}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-scoped"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, mockClient, rules, "scoped")
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-scoped", saName)
	mockClient.AssertExpectations(t)
}

// Test_ensureScopedProviderServiceAccountAndRBAC_usesDefaultNamespace checks that the ServiceAccount and
// ClusterRoleBinding subjects use the default namespace ("default").
func Test_ensureScopedProviderServiceAccountAndRBAC_usesDefaultNamespace(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	rules := []rbacv1.PolicyRule{}

	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Namespace == "default" && sa.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == "platform-mesh-provider-scoped"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-provider-scoped" && len(crb.Subjects) == 1 && crb.Subjects[0].Namespace == "default"
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-workspace-access-scoped"}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == "platform-mesh-workspace-access-scoped" && len(crb.Subjects) == 1 && crb.Subjects[0].Namespace == "default"
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, mockClient, rules, "scoped")
	require.NoError(t, err)
	assert.Equal(t, "platform-mesh-provider-scoped", saName)
	mockClient.AssertExpectations(t)
}

// Test_sanitizeSecretNameForRBAC checks that secret names are safe for K8s resource names.
func Test_sanitizeSecretNameForRBAC(t *testing.T) {
	tests := []struct {
		secret string
		want   string
	}{
		{"rebac-authz-webhook-kubeconfig", "rebac-authz-webhook-kubeconfig"},
		{"extension-manager-operator-kubeconfig", "extension-manager-operator-kubeconfig"},
		{"UPPER_and.mixed", "upper-and-mixed"},
		{"", "scoped"},
		{"a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.secret, func(t *testing.T) {
			got := sanitizeSecretNameForRBAC(tt.secret)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test_ensureScopedProviderServiceAccountAndRBAC_perProviderSuffix checks that a custom provider suffix yields per-provider SA/CR names.
func Test_ensureScopedProviderServiceAccountAndRBAC_perProviderSuffix(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	rules := []rbacv1.PolicyRule{{APIGroups: []string{"foo"}, Resources: []string{"bars"}, Verbs: []string{"get"}}}
	suffix := "rebac-authz-webhook-kubeconfig"
	saNameWant := "platform-mesh-provider-rebac-authz-webhook-kubeconfig"
	crNameWant := saNameWant
	workspaceCRBWant := "platform-mesh-workspace-access-rebac-authz-webhook-kubeconfig"

	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		sa, ok := obj.(*corev1.ServiceAccount)
		return ok && sa.Name == saNameWant
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		cr, ok := obj.(*rbacv1.ClusterRole)
		return ok && cr.Name == crNameWant
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: crNameWant}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == crNameWant && crb.RoleRef.Name == crNameWant
	}), mock.Anything).Return(nil).Once()
	mockClient.EXPECT().Get(mock.Anything, types.NamespacedName{Name: workspaceCRBWant}, mock.AnythingOfType("*v1.ClusterRoleBinding")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "x")).Once()
	mockClient.EXPECT().Create(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		crb, ok := obj.(*rbacv1.ClusterRoleBinding)
		return ok && crb.Name == workspaceCRBWant && crb.Subjects[0].Name == saNameWant
	}), mock.Anything).Return(nil).Once()

	saName, err := ensureScopedProviderServiceAccountAndRBAC(ctx, mockClient, rules, suffix)
	require.NoError(t, err)
	assert.Equal(t, saNameWant, saName)
	mockClient.AssertExpectations(t)
}

// Test_getExtraPolicyRulesFromFlags checks that enabled flags return the correct rule blocks.
func Test_getExtraPolicyRulesFromFlags(t *testing.T) {
	trueVal := true
	falseVal := false
	tests := []struct {
		name     string
		flags    *ExtraPolicyRulesFlags
		wantLen  int
		wantAPIs []string
	}{
		{"nil flags", nil, 0, nil},
		{"both false", &ExtraPolicyRulesFlags{EnableGetLogicalCluster: &falseVal, EnableStoresAccess: &falseVal}, 0, nil},
		{"get logical cluster only", &ExtraPolicyRulesFlags{EnableGetLogicalCluster: &trueVal}, 1, []string{"core.kcp.io"}},
		{"stores only", &ExtraPolicyRulesFlags{EnableStoresAccess: &trueVal}, 1, []string{"core.platform-mesh.io"}},
		{"both enabled", &ExtraPolicyRulesFlags{EnableGetLogicalCluster: &trueVal, EnableStoresAccess: &trueVal}, 2, []string{"core.kcp.io", "core.platform-mesh.io"}},
		{"workspace types only", &ExtraPolicyRulesFlags{EnableWorkspaceTypesAccess: &trueVal}, 1, []string{"tenancy.kcp.io"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := getExtraPolicyRulesFromFlags(tt.flags)
			assert.Len(t, rules, tt.wantLen)
			for i, apiGroup := range tt.wantAPIs {
				require.Greater(t, len(rules), i)
				assert.Equal(t, []string{apiGroup}, rules[i].APIGroups)
			}
		})
	}
}
