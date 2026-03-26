package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	// Same as runOperator appConfig.KCP.ClusterAdminSecretName / operator default (not kubeconfig-kcp-admin from Kubeconfig CR).
	e2eKcpClusterAdminClientCertSecretName = "kcp-cluster-admin-client-cert"

	// Operator-written Secrets for extraProviderConnections in platform-mesh.yaml (provider1 vs provider2 scenario).
	e2eScopedKubeconfigProvider1SecretName = "kind-e2e-provider1-scoped-kubeconfig"
	e2eScopedKubeconfigProvider2SecretName = "kind-e2e-provider2-scoped-kubeconfig"
	e2eAdminKubeconfigProvider3SecretName  = "kind-e2e-provider3-admin-kubeconfig"

	// APIExport / APIExportEndpointSlice name in yaml/kcp-provider-workspaces; kubectl resource for E2EProviderConfig.
	e2eKindScopedProviderExportName       = "kind-e2e-scoped-provider.platform-mesh.io"
	e2eScopedProviderConfigResource       = "e2eproviderconfigs.kind-e2e-scoped-provider.platform-mesh.io"
	e2eKindScopedProviderConfigAPIVersion = "kind-e2e-scoped-provider.platform-mesh.io/v1alpha1"

	// Match test/e2e/kind/yaml/platform-mesh-resource/platform-mesh.yaml extraProviderConnections[].path;
	// ProvidersecretSubroutine creates scoped SA + ClusterRole + ClusterRoleBinding in each provider workspace.
	e2eScopedKubeconfigProvider1Path = "root:providers:provider1"
	e2eScopedKubeconfigProvider2Path = "root:providers:provider2"

	e2eKcpProviderWorkspacesYAMLDir = "../../../test/e2e/kind/yaml/kcp-provider-workspaces"
)

// setupScopedProviderKcpBeforePlatformMesh must run from SetupSuite before platform-mesh.yaml is applied:
// extraProviderConnections need APIExports/slices in kcp; kcp clients use the same TLS material as the operator
// (kcp-cluster-admin-client-cert), not kubeconfig-kcp-admin (optional Kubeconfig CR output).
// It cannot run inside TestScopedKubeconfig* — the operator already reconciles PlatformMesh after SetupSuite.
func (s *KindTestSuite) setupScopedProviderKcpBeforePlatformMesh(ctx context.Context) {
	s.waitForKcpClusterAdminClientCert(ctx)
	s.ensureScopedE2EKcpProviderWorkspaces(ctx)
}

// Provider1: APIExport + schema + endpoint slice come from setupScopedProviderKcpBeforePlatformMesh (yaml/kcp-provider-workspaces), like a
// pre-provisioned workspace. The test uses only the operator-written scoped kubeconfig to create an E2EProviderConfig
// instance for that export (virtual workspace server).
func (s *KindTestSuite) TestScopedKubeconfigProvider1() {
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	sec := s.requireE2EProviderKubeconfigSecret(ctx, e2eScopedKubeconfigProvider1SecretName)
	kcfg := sec.Data["kubeconfig"]

	name := fmt.Sprintf("e2e-provider1-%d", time.Now().UnixNano())
	note := "scoped-kubeconfig-provider1-e2e"
	manifestPath := filepath.Join(s.T().TempDir(), "e2eproviderconfig-provider1.yaml")
	manifest := fmt.Sprintf(`apiVersion: %s
kind: E2EProviderConfig
metadata:
  name: %s
spec:
  note: %s
`, e2eKindScopedProviderConfigAPIVersion, name, note)
	s.Require().NoError(os.WriteFile(manifestPath, []byte(manifest), 0o600))

	_, err := s.runKubectlWithRawKubeconfig(kcfg, "apply", "-f", manifestPath)
	s.Require().NoError(err, "kubectl apply E2EProviderConfig with operator-generated provider1 scoped kubeconfig")

	out, err := s.runKubectlWithRawKubeconfig(kcfg, "get", e2eScopedProviderConfigResource, name, "-o", "jsonpath={.spec.note}")
	s.Require().NoError(err)
	s.Require().Equal(note, strings.TrimSpace(string(out)))

	s.deleteE2EProviderConfigOrWarn(ctx, e2eScopedKubeconfigProvider1Path, name)
}

// Provider2: same as Provider1 regarding pre-provisioned export YAML; scoped kubeconfig uses workspace cluster URL.
// Test creates an E2EProviderConfig resource with that kubeconfig only (no APIExport creation in the test).
func (s *KindTestSuite) TestScopedKubeconfigProvider2() {
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	sec := s.requireE2EProviderKubeconfigSecret(ctx, e2eScopedKubeconfigProvider2SecretName)
	kcfg := sec.Data["kubeconfig"]

	name := fmt.Sprintf("e2e-provider2-%d", time.Now().UnixNano())
	note := "scoped-kubeconfig-provider2-e2e"
	manifestPath := filepath.Join(s.T().TempDir(), "e2eproviderconfig-provider2.yaml")
	manifest := fmt.Sprintf(`apiVersion: %s
kind: E2EProviderConfig
metadata:
  name: %s
spec:
  note: %s
`, e2eKindScopedProviderConfigAPIVersion, name, note)
	s.Require().NoError(os.WriteFile(manifestPath, []byte(manifest), 0o600))

	_, err := s.runKubectlWithRawKubeconfig(kcfg, "apply", "-f", manifestPath)
	s.Require().NoError(err, "kubectl apply E2EProviderConfig with operator-generated provider2 scoped kubeconfig")

	out, err := s.runKubectlWithRawKubeconfig(kcfg, "get", e2eScopedProviderConfigResource, name, "-o", "jsonpath={.spec.note}")
	s.Require().NoError(err)
	s.Require().Equal(note, strings.TrimSpace(string(out)))

	s.deleteE2EProviderConfigOrWarn(ctx, e2eScopedKubeconfigProvider2Path, name)
}

// Provider3: extraProviderConnections entry with adminAuth true — same slice-based virtual workspace wiring as default providers, admin cert material.
func (s *KindTestSuite) TestExtraProviderAdminKubeconfigProvider3() {
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	sec := s.requireE2EProviderKubeconfigSecret(ctx, e2eAdminKubeconfigProvider3SecretName)
	cfg, err := clientcmd.Load(sec.Data["kubeconfig"])
	s.Require().NoError(err)
	clusterName := cfg.Contexts[cfg.CurrentContext].Cluster
	cluster := cfg.Clusters[clusterName]
	s.Require().NotNil(cluster)
	s.Require().Contains(cluster.Server, "front-proxy", "admin provider kubeconfig should use front-proxy host from operator rewrite")
}

// requireE2EProviderKubeconfigSecret loads the operator-written provider secret (PlatformMesh extraProviderConnections[].secret).
// Call only after scopedWaitPlatformMeshReady: Ready implies ProvidersecretSubroutine has populated these secrets.
func (s *KindTestSuite) requireE2EProviderKubeconfigSecret(ctx context.Context, secretName string) *corev1.Secret {
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: e2ePlatformMeshNamespace,
	}, sec)
	s.Require().NoError(err, "provider kubeconfig secret %s/%s must exist once PlatformMesh is Ready", e2ePlatformMeshNamespace, secretName)
	s.Require().NotEmpty(sec.Data["kubeconfig"], "secret %s/%s must contain non-empty kubeconfig data", e2ePlatformMeshNamespace, secretName)
	return sec
}

func (s *KindTestSuite) deleteE2EProviderConfigOrWarn(ctx context.Context, workspacePath, name string) {
	cl, err := s.kcpClientForWorkspace(ctx, workspacePath)
	if err != nil {
		s.logger.Warn().Err(err).Str("workspace", workspacePath).Msg("cleanup: no kcp client for E2EProviderConfig delete")
		return
	}
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(e2eKindScopedProviderConfigAPIVersion)
	obj.SetKind("E2EProviderConfig")
	obj.SetName(name)
	if err := cl.Delete(ctx, obj); err != nil {
		s.logger.Warn().Err(err).Str("name", name).Str("workspace", workspacePath).Msg("cleanup delete E2EProviderConfig")
	}
}

func (s *KindTestSuite) kcpAdminKubeconfigBytes(ctx context.Context) ([]byte, error) {
	var sec corev1.Secret
	if err := s.client.Get(ctx, client.ObjectKey{
		Name:      e2eKcpClusterAdminClientCertSecretName,
		Namespace: e2ePlatformMeshNamespace,
	}, &sec); err != nil {
		return nil, err
	}
	if sec.Data == nil {
		return nil, fmt.Errorf("secret %s/%s has no Data", e2ePlatformMeshNamespace, e2eKcpClusterAdminClientCertSecretName)
	}
	ca, crt, key := sec.Data["ca.crt"], sec.Data["tls.crt"], sec.Data["tls.key"]
	if len(ca) == 0 || len(crt) == 0 || len(key) == 0 {
		return nil, fmt.Errorf("secret %s/%s missing ca.crt, tls.crt, or tls.key", e2ePlatformMeshNamespace, e2eKcpClusterAdminClientCertSecretName)
	}
	apiCfg := clientcmdapi.NewConfig()
	apiCfg.Clusters = map[string]*clientcmdapi.Cluster{
		"kcp": {
			Server:                   kcpLocalFrontProxyURL,
			CertificateAuthorityData: ca,
		},
	}
	apiCfg.Contexts = map[string]*clientcmdapi.Context{
		"admin": {Cluster: "kcp", AuthInfo: "admin"},
	}
	apiCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"admin": {ClientCertificateData: crt, ClientKeyData: key},
	}
	apiCfg.CurrentContext = "admin"
	return clientcmd.Write(*apiCfg)
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

// waitForKcpClusterAdminClientCert polls until kcp-cluster-admin-client-cert has TLS material (same as operator buildKubeconfig path).
func (s *KindTestSuite) waitForKcpClusterAdminClientCert(ctx context.Context) {
	sec := &corev1.Secret{}
	s.Eventually(func() bool {
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      e2eKcpClusterAdminClientCertSecretName,
			Namespace: e2ePlatformMeshNamespace,
		}, sec)
		if err != nil {
			s.logger.Warn().Err(err).Str("secret", e2eKcpClusterAdminClientCertSecretName).
				Msg("kcp cluster-admin client cert secret not available yet")
			return false
		}
		if sec.Data == nil {
			return false
		}
		if len(sec.Data["ca.crt"]) == 0 || len(sec.Data["tls.crt"]) == 0 || len(sec.Data["tls.key"]) == 0 {
			s.logger.Warn().Str("secret", e2eKcpClusterAdminClientCertSecretName).
				Msg("kcp cluster-admin client cert secret missing ca.crt / tls.crt / tls.key")
			return false
		}
		return true
	}, 20*time.Minute, 10*time.Second, "Secret "+e2eKcpClusterAdminClientCertSecretName+" was not populated (needed for kcp workspace setup before PlatformMesh apply)")
}

// ensureScopedE2EKcpProviderWorkspaces creates root:providers:provider1|provider2 and applies static YAML that models a
// real deployment: APIExport (+ APIResourceSchema + APIExportEndpointSlice) already exists before tests run; tests only
// exercise creating resources from that export via scoped kubeconfigs.
func (s *KindTestSuite) ensureScopedE2EKcpProviderWorkspaces(ctx context.Context) {
	emptyTmpl := make(map[string]string)
	rootClient, err := s.kcpClientForWorkspace(ctx, "root")
	s.Require().NoError(err, "kcp client for root")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "workspace-providers.yaml"), rootClient, emptyTmpl),
		"apply workspace-providers.yaml",
	)
	s.waitWorkspaceReady(ctx, rootClient, "providers")
	providersClient, err := s.kcpClientForWorkspace(ctx, "root:providers")
	s.Require().NoError(err, "kcp client for root:providers")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "workspace-provider1-provider2.yaml"), providersClient, emptyTmpl),
		"apply workspace-provider1-provider2.yaml",
	)
	s.waitWorkspaceReady(ctx, providersClient, "provider1")
	s.waitWorkspaceReady(ctx, providersClient, "provider2")

	provider1Client, err := s.kcpClientForWorkspace(ctx, e2eScopedKubeconfigProvider1Path)
	s.Require().NoError(err, "kcp client for provider1")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "provider1-kind-e2e-scoped-provider-export.yaml"), provider1Client, emptyTmpl),
		"apply root:providers:provider1 "+e2eKindScopedProviderExportName+" export manifests",
	)

	provider2Client, err := s.kcpClientForWorkspace(ctx, e2eScopedKubeconfigProvider2Path)
	s.Require().NoError(err, "kcp client for provider2")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "provider2-kind-e2e-scoped-provider-export.yaml"), provider2Client, emptyTmpl),
		"apply root:providers:provider2 "+e2eKindScopedProviderExportName+" export manifests",
	)
}

// kubectl with kubeconfig bytes unchanged (virtual workspace or workspace cluster URL as written by the operator).
func (s *KindTestSuite) runKubectlWithRawKubeconfig(kubeconfigBytes []byte, kubectlArgs ...string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "scoped-kubeconfig-raw-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(kubeconfigBytes); err != nil {
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
