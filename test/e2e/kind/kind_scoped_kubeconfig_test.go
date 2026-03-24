package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

func (s *KindTestSuite) TestScopedKubeconfigAPIBindingWorkspaceBoundaries() {
	ctx := context.TODO()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	scopedSecretName := "e2e-scoped-kubeconfig-" + suffix
	orgName := "e2e-scoped-org-" + suffix
	accountName := "e2e-scoped-account-" + suffix
	exportName := "scoped-kcfg-export-" + suffix
	bindingName := "scoped-kcfg-binding-" + suffix
	denyWorkspaceName := "scoped-deny-check-" + suffix

	rootClient := s.mustKcpClientForWorkspace(ctx, "root")
	orgsClient := s.mustKcpClientForWorkspace(ctx, "root:orgs")

	// Create a workspace outside the allowed root:orgs tree to validate denial.
	s.applyWorkspace(ctx, rootClient, denyWorkspaceName, "orgs", "root")
	s.waitWorkspaceReady(ctx, rootClient, denyWorkspaceName)

	// Create org + account workspaces under root:orgs to validate allowed access.
	s.applyWorkspace(ctx, orgsClient, orgName, "org", "root")
	s.waitWorkspaceReady(ctx, orgsClient, orgName)

	orgWorkspacePath := "root:orgs:" + orgName
	orgWorkspaceClient := s.mustKcpClientForWorkspace(ctx, orgWorkspacePath)
	s.applyWorkspace(ctx, orgWorkspaceClient, accountName, "account", "root")
	s.waitWorkspaceReady(ctx, orgWorkspaceClient, accountName)

	accountWorkspacePath := orgWorkspacePath + ":" + accountName
	accountWorkspaceClient := s.mustKcpClientForWorkspace(ctx, accountWorkspacePath)

	// Create APIExport in org workspace and APIBinding in account workspace.
	s.applyUnstructured(ctx, orgWorkspaceClient, &unstructured.Unstructured{
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
	})

	s.applyUnstructured(ctx, accountWorkspaceClient, &unstructured.Unstructured{
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
	})

	// Create an isolated scoped kubeconfig for this test after org/account exist so RootOrgAccess
	// can bind access for current child workspaces as part of secret generation.
	scopedSecret := s.createScopedProviderSecret(ctx, scopedSecretName)

	s.Eventually(func() bool {
		out, err := s.runScopedKubectl(scopedSecret.Data["kubeconfig"], accountWorkspacePath, "get", "apibinding.apis.kcp.io", bindingName, "-o", "jsonpath={.status.phase}")
		return err == nil && strings.TrimSpace(string(out)) == "Bound"
	}, 3*time.Minute, 5*time.Second, "scoped kubeconfig could not access allowed APIBinding workspace")

	out, err := s.runScopedKubectl(scopedSecret.Data["kubeconfig"], orgWorkspacePath, "get", "apiexport.apis.kcp.io", exportName, "-o", "jsonpath={.metadata.name}")
	s.Require().NoError(err, "scoped kubeconfig should access APIExport in allowed workspace")
	s.Equal(exportName, strings.TrimSpace(string(out)))

	_, err = s.runScopedKubectl(scopedSecret.Data["kubeconfig"], "root:"+denyWorkspaceName, "get", "logicalclusters.core.kcp.io", "cluster", "-o", "name")
	s.Error(err, "scoped kubeconfig must not access unrelated workspace")
}

func (s *KindTestSuite) createScopedProviderSecret(ctx context.Context, secretName string) *corev1.Secret {
	adminKubeconfig := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      "kubeconfig-kcp-admin",
		Namespace: "platform-mesh-system",
	}, adminKubeconfig)
	s.Require().NoError(err, "failed to read kubeconfig-kcp-admin")
	s.Require().NotEmpty(adminKubeconfig.Data["kubeconfig"], "kubeconfig-kcp-admin is missing kubeconfig data")

	cfg, err := clientcmd.RESTConfigFromKubeConfig(adminKubeconfig.Data["kubeconfig"])
	s.Require().NoError(err, "failed to build rest config from kcp admin kubeconfig")
	cfg = rest.CopyConfig(cfg)
	cfg.Host = "https://localhost:8443"

	r := subroutines.NewProviderSecretSubroutine(s.client, &subroutines.Helper{}, subroutines.DefaultHelmGetter{}, cfg.Host)
	pc, ok := scopedIAMProviderConnectionForTest(secretName)
	s.Require().True(ok, "iam-service-kubeconfig default provider connection not found")
	_, opErr := r.HandleProviderConnection(ctx, &corev1alpha1.PlatformMesh{}, pc, cfg)
	s.Require().Nil(opErr, "failed to generate scoped provider kubeconfig secret")

	scopedSecret := &corev1.Secret{}
	s.Eventually(func() bool {
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      secretName,
			Namespace: "platform-mesh-system",
		}, scopedSecret)
		return err == nil && len(scopedSecret.Data["kubeconfig"]) > 0
	}, 2*time.Minute, 2*time.Second, "scoped kubeconfig secret was not created")
	return scopedSecret
}

func scopedIAMProviderConnectionForTest(secretName string) (corev1alpha1.ProviderConnection, bool) {
	for _, pc := range subroutines.DefaultProviderConnections {
		if pc.Secret != "iam-service-kubeconfig" {
			continue
		}
		copied := pc
		copied.Secret = secretName
		return copied, true
	}
	return corev1alpha1.ProviderConnection{}, false
}

func (s *KindTestSuite) mustKcpClientForWorkspace(ctx context.Context, workspacePath string) client.Client {
	adminKubeconfig := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      "kubeconfig-kcp-admin",
		Namespace: "platform-mesh-system",
	}, adminKubeconfig)
	s.Require().NoError(err, "failed to read kubeconfig-kcp-admin")
	s.Require().NotEmpty(adminKubeconfig.Data["kubeconfig"], "kubeconfig-kcp-admin is missing kubeconfig data")

	cfg, err := clientcmd.RESTConfigFromKubeConfig(adminKubeconfig.Data["kubeconfig"])
	s.Require().NoError(err, "failed to build rest config from kcp admin kubeconfig")
	cfg = rest.CopyConfig(cfg)
	// e2e test process runs outside the cluster network; localhost reaches front-proxy via kind config.
	cfg.Host = "https://localhost:8443"

	kcpClient, err := (&subroutines.Helper{}).NewKcpClient(cfg, workspacePath)
	s.Require().NoError(err, "failed to create kcp client for workspace "+workspacePath)
	return kcpClient
}

func (s *KindTestSuite) applyWorkspace(ctx context.Context, cl client.Client, name, workspaceType, workspaceTypePath string) {
	s.applyUnstructured(ctx, cl, &unstructured.Unstructured{
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
	})
}

func (s *KindTestSuite) waitWorkspaceReady(ctx context.Context, cl client.Client, workspaceName string) {
	s.Eventually(func() bool {
		ws := &unstructured.Unstructured{}
		ws.SetAPIVersion("tenancy.kcp.io/v1alpha1")
		ws.SetKind("Workspace")
		if err := cl.Get(ctx, client.ObjectKey{Name: workspaceName}, ws); err != nil {
			return false
		}
		phase, found, err := unstructured.NestedString(ws.Object, "status", "phase")
		return err == nil && found && phase == "Ready"
	}, 3*time.Minute, 5*time.Second, "workspace "+workspaceName+" did not become ready")
}

func (s *KindTestSuite) applyUnstructured(ctx context.Context, cl client.Client, obj *unstructured.Unstructured) {
	err := cl.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner("platform-mesh-operator-e2e"))
	s.Require().NoError(err, "failed applying object %s/%s", obj.GetKind(), obj.GetName())
}

func (s *KindTestSuite) runScopedKubectl(baseKubeconfig []byte, workspacePath string, kubectlArgs ...string) ([]byte, error) {
	cfg, err := clientcmd.Load(baseKubeconfig)
	if err != nil {
		return nil, err
	}

	workspaceURL := fmt.Sprintf("https://localhost:8443/clusters/%s", workspacePath)
	for _, c := range cfg.Clusters {
		if c != nil {
			c.Server = workspaceURL
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

	args := []string{"--kubeconfig", tmp.Name()}
	args = append(args, kubectlArgs...)
	return runCommand("kubectl", args...)
}
