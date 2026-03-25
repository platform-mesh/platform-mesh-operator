package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Matches suite_kind_test runOperator KCP.Url and Kind object keys used elsewhere in this package.
const (
	kcpLocalFrontProxyURL = "https://localhost:8443"

	e2ePlatformMeshNamespace = "platform-mesh-system"
	e2ePlatformMeshName      = "platform-mesh"

	e2eKcpAdminSecretName = "kubeconfig-kcp-admin"

	// Same secret name as spec.kcp.extraProviderConnections in test/e2e/kind/yaml/platform-mesh-resource/platform-mesh.yaml.
	e2eScopedKubeconfigSecretName = "e2e-scoped-kubeconfig"
)

// Kind e2e — scoped provider kubeconfig (this file). We test:
//   - Operator writes Secret from spec.kcp.extraProviderConnections in yaml/platform-mesh-resource/platform-mesh.yaml.
//   - Secret kubeconfig parses (cluster + auth).
//   - kubectl with that kubeconfig succeeds for org/account + APIExport/APIBinding; fails for unrelated root workspace.
func (s *KindTestSuite) TestScopedKubeconfigAPIBindingWorkspaceBoundaries() {
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	// Operator-created secret (adminAuth false + endpointSliceName in applied PlatformMesh).
	scopedSecret := s.waitForOperatorScopedKubeconfigSecret(ctx)
	s.assertScopedKubeconfigSecretValid(scopedSecret)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	orgName := "e2e-scoped-org-" + suffix
	accountName := "e2e-scoped-account-" + suffix
	exportName := "scoped-kcfg-export-" + suffix
	bindingName := "scoped-kcfg-binding-" + suffix
	denyWorkspaceName := "scoped-deny-check-" + suffix

	// Admin kubeconfig clients to create workspaces and API objects (not the scoped credential under test).
	rootClient, err := s.kcpClientForWorkspace(ctx, "root")
	s.Assert().NoError(err, "kcp client for root")
	orgsClient, err := s.kcpClientForWorkspace(ctx, "root:orgs")
	s.Assert().NoError(err, "kcp client for root:orgs")

	// Workspace outside root:orgs tree — later we assert scoped token cannot use it.
	s.applyWorkspace(ctx, rootClient, denyWorkspaceName, "orgs", "root")
	s.waitWorkspaceReady(ctx, rootClient, denyWorkspaceName)

	s.applyWorkspace(ctx, orgsClient, orgName, "org", "root")
	s.waitWorkspaceReady(ctx, orgsClient, orgName)

	orgWorkspacePath := "root:orgs:" + orgName
	orgWorkspaceClient, err := s.kcpClientForWorkspace(ctx, orgWorkspacePath)
	s.Assert().NoError(err, "kcp client for org workspace")
	s.applyWorkspace(ctx, orgWorkspaceClient, accountName, "account", "root")
	s.waitWorkspaceReady(ctx, orgWorkspaceClient, accountName)

	accountWorkspacePath := orgWorkspacePath + ":" + accountName
	accountWorkspaceClient, err := s.kcpClientForWorkspace(ctx, accountWorkspacePath)
	s.Assert().NoError(err, "kcp client for account workspace")

	// Export in org, binding in account — resources scoped token should see when pointed at those workspaces.
	s.Assert().NoError(s.applyUnstructuredSSA(ctx, orgWorkspaceClient, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apis.kcp.io/v1alpha1",
			"kind":       "APIExport",
			"metadata": map[string]interface{}{
				"name": exportName,
			},
			"spec": map[string]interface{}{
				"latestResourceSchemas": []interface{}{},
			},
		},
	}))

	s.Assert().NoError(s.applyUnstructuredSSA(ctx, accountWorkspaceClient, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apis.kcp.io/v1alpha1",
			"kind":       "APIBinding",
			"metadata": map[string]interface{}{
				"name": bindingName,
			},
			"spec": map[string]interface{}{
				"reference": map[string]interface{}{
					"export": map[string]interface{}{
						"path": orgWorkspacePath,
						"name": exportName,
					},
				},
			},
		},
	}))

	// Point kubectl at a workspace by rewriting cluster.server to .../clusters/<path> (same token each time).
	clusterURL := func(workspacePath string) string {
		return fmt.Sprintf("%s/clusters/%s", kcpLocalFrontProxyURL, workspacePath)
	}

	// Allow: scoped token can read binding in account workspace.
	s.Eventually(func() bool {
		out, err := s.runKubectlWithClusterServer(scopedSecret.Data["kubeconfig"], clusterURL(accountWorkspacePath), "get", "apibinding.apis.kcp.io", bindingName, "-o", "jsonpath={.status.phase}")
		if err != nil {
			s.logger.Warn().Err(err).Msg("kubectl apibinding check failed")
			return false
		}
		return strings.TrimSpace(string(out)) == "Bound"
	}, 20*time.Minute, 10*time.Second, "scoped kubeconfig could not access allowed APIBinding workspace")

	// Allow: read APIExport in org workspace.
	out, err := s.runKubectlWithClusterServer(scopedSecret.Data["kubeconfig"], clusterURL(orgWorkspacePath), "get", "apiexport.apis.kcp.io", exportName, "-o", "jsonpath={.metadata.name}")
	s.Assert().NoError(err, "scoped kubeconfig should access APIExport in allowed workspace")
	s.Equal(exportName, strings.TrimSpace(string(out)))

	// Deny: same token must not access unrelated root:<deny> workspace.
	_, err = s.runKubectlWithClusterServer(scopedSecret.Data["kubeconfig"], clusterURL("root:"+denyWorkspaceName), "get", "logicalclusters.core.kcp.io", "cluster", "-o", "name")
	s.Require().Error(err, "scoped kubeconfig must not access unrelated workspace")
}

// Wait until reconciler writes kubeconfig for the extraProviderConnections entry in e2e platform-mesh.yaml.
func (s *KindTestSuite) waitForOperatorScopedKubeconfigSecret(ctx context.Context) *corev1.Secret {
	sec := &corev1.Secret{}
	s.Eventually(func() bool {
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      e2eScopedKubeconfigSecretName,
			Namespace: e2ePlatformMeshNamespace,
		}, sec)
		if err != nil {
			s.logger.Warn().Err(err).Str("secret", e2eScopedKubeconfigSecretName).Msg("scoped kubeconfig secret not created by operator yet")
			return false
		}
		if len(sec.Data["kubeconfig"]) == 0 {
			s.logger.Warn().Str("secret", e2eScopedKubeconfigSecretName).Msg("scoped kubeconfig secret missing kubeconfig data")
			return false
		}
		return true
	}, 20*time.Minute, 10*time.Second, "Secret "+e2eScopedKubeconfigSecretName+" from PlatformMesh.spec.kcp.extraProviderConnections was not populated by the operator")
	return sec
}

// Sanity-check secret content parses as a kubeconfig with cluster + auth.
func (s *KindTestSuite) assertScopedKubeconfigSecretValid(sec *corev1.Secret) {
	raw := sec.Data["kubeconfig"]
	s.Assert().NotEmpty(raw, "Secret %q must contain kubeconfig data", sec.Name)
	cfg, err := clientcmd.Load(raw)
	s.Assert().NoError(err, "scoped kubeconfig must parse")
	s.Assert().NotEmpty(cfg.Clusters, "scoped kubeconfig must define at least one cluster")
	s.Assert().NotEmpty(cfg.AuthInfos, "scoped kubeconfig must define at least one user/credential")

	var cluster *clientcmdapi.Cluster
	for _, c := range cfg.Clusters {
		if c != nil {
			cluster = c
			break
		}
	}
	s.Assert().NotNil(cluster, "scoped kubeconfig must have a non-nil cluster entry")
	s.Assert().NotEmpty(cluster.Server, "scoped kubeconfig cluster must set server URL")
}

func (s *KindTestSuite) kcpAdminKubeconfigBytes(ctx context.Context) ([]byte, error) {
	var sec corev1.Secret
	if err := s.client.Get(ctx, client.ObjectKey{Name: e2eKcpAdminSecretName, Namespace: e2ePlatformMeshNamespace}, &sec); err != nil {
		return nil, err
	}
	raw := sec.Data["kubeconfig"]
	if len(raw) == 0 {
		return nil, fmt.Errorf("secret %s/%s has no kubeconfig key", e2ePlatformMeshNamespace, e2eKcpAdminSecretName)
	}
	return raw, nil
}

func (s *KindTestSuite) restConfigForLocalKCPFrontProxy(kubeconfigBytes []byte) (*rest.Config, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, err
	}
	out := rest.CopyConfig(cfg)
	out.Host = kcpLocalFrontProxyURL
	return out, nil
}

func (s *KindTestSuite) kcpClientForWorkspace(ctx context.Context, workspacePath string) (client.Client, error) {
	raw, err := s.kcpAdminKubeconfigBytes(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.restConfigForLocalKCPFrontProxy(raw)
	if err != nil {
		return nil, err
	}
	return (&subroutines.Helper{}).NewKcpClient(cfg, workspacePath)
}

func (s *KindTestSuite) applyUnstructuredSSA(ctx context.Context, cl client.Client, obj *unstructured.Unstructured) error {
	return cl.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner("platform-mesh-operator-e2e"))
}

func (s *KindTestSuite) applyWorkspace(ctx context.Context, cl client.Client, name, workspaceType, workspaceTypePath string) {
	s.Assert().NoError(s.applyUnstructuredSSA(ctx, cl, &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "tenancy.kcp.io/v1alpha1",
			"kind":       "Workspace",
			"metadata": map[string]interface{}{
				"name": name,
			},
			"spec": map[string]interface{}{
				"type": map[string]interface{}{
					"name": workspaceType,
					"path": workspaceTypePath,
				},
			},
		},
	}))
}

// Temp kubeconfig with a single cluster server URL, then kubectl (for host-side e2e process).
func (s *KindTestSuite) runKubectlWithClusterServer(kubeconfigBytes []byte, clusterServer string, kubectlArgs ...string) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return nil, err
	}
	for _, c := range cfg.Clusters {
		if c != nil {
			c.Server = clusterServer
		}
	}
	rendered, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "scoped-kubeconfig-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(rendered); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	args := append([]string{"--kubeconfig", tmp.Name()}, kubectlArgs...)
	return runCommand("kubectl", args...)
}

func (s *KindTestSuite) waitWorkspaceReady(ctx context.Context, cl client.Client, workspaceName string) {
	s.Eventually(func() bool {
		ws := &unstructured.Unstructured{}
		ws.SetAPIVersion("tenancy.kcp.io/v1alpha1")
		ws.SetKind("Workspace")
		if err := cl.Get(ctx, client.ObjectKey{Name: workspaceName}, ws); err != nil {
			s.logger.Warn().Err(err).Str("workspace", workspaceName).Msg("workspace not ready yet")
			return false
		}
		phase, found, err := unstructured.NestedString(ws.Object, "status", "phase")
		if err != nil || !found || phase != "Ready" {
			return false
		}
		return true
	}, 3*time.Minute, 10*time.Second, "workspace "+workspaceName+" did not become ready")
}

// Same Ready gate as TestResourceReady; scoped test only (not shared with other test files).
func (s *KindTestSuite) scopedWaitPlatformMeshReady(ctx context.Context) {
	s.Eventually(func() bool {
		pm := corev1alpha1.PlatformMesh{}
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      e2ePlatformMeshName,
			Namespace: e2ePlatformMeshNamespace,
		}, &pm)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to get Platform Mesh resource")
			return false
		}
		for _, condition := range pm.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				s.logger.Info().Msg("PlatformMesh resource is ready")
				return true
			}
		}
		return false
	}, 25*time.Minute, 10*time.Second)
}
