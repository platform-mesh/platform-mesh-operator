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
	kerrors "k8s.io/apimachinery/pkg/api/errors"
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

	// Infra chart Certificate secretName; may live in platform-mesh-system or kcp-system (.Values.kcp.namespace default).
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

// scopedE2EKcpAdminCertSecretNamespace is set by waitForKcpClusterAdminClientCert in this file only.
var scopedE2EKcpAdminCertSecretNamespace string

var scopedE2EKcpClusterAdminCertSearchNamespaces = []string{
	e2ePlatformMeshNamespace,
	"kcp-system",
}

// Test03ScopedKubeconfigKcpPrereq waits for kcp-cluster-admin-client-cert (TLS material for kcp clients) and seeds
// root:providers:provider1|provider2 with APIExports/slices before Test04/05ScopedKubeconfigProvider1/2.
// Runs after Test01ResourceReady and Test02ExtraWorkspaces via the TestNN prefix ordering convention.
func (s *KindTestSuite) Test03ScopedKubeconfigKcpPrereq() {
	s.logger.Info().Str("kind_e2e", "Test03ScopedKubeconfigKcpPrereq").Msg("start")
	ctx := context.Background()
	s.waitForKcpClusterAdminClientCert(ctx)
	s.ensureScopedE2EKcpProviderWorkspaces(ctx)
	s.logger.Info().Str("kind_e2e", "Test03ScopedKubeconfigKcpPrereq").Msg("done")
}

// Provider1: APIExport + schema + endpoint slice come from Test03ScopedKubeconfigKcpPrereq (yaml/kcp-provider-workspaces), like a
// pre-provisioned workspace. The test uses only the operator-written scoped kubeconfig to create an E2EProviderConfig
// instance for that export (virtual workspace server).
func (s *KindTestSuite) Test04ScopedKubeconfigProvider1() {
	s.logger.Info().Str("kind_e2e", "Test04ScopedKubeconfigProvider1").Msg("start")
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
	s.logger.Info().Str("kind_e2e", "Test04ScopedKubeconfigProvider1").Str("e2eproviderconfig", name).Msg("done")
}

// Provider2: same as Provider1 regarding pre-provisioned export YAML; scoped kubeconfig uses workspace cluster URL.
// Test creates an E2EProviderConfig resource with that kubeconfig only (no APIExport creation in the test).
func (s *KindTestSuite) Test05ScopedKubeconfigProvider2() {
	s.logger.Info().Str("kind_e2e", "Test05ScopedKubeconfigProvider2").Msg("start")
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
	s.logger.Info().Str("kind_e2e", "Test05ScopedKubeconfigProvider2").Str("e2eproviderconfig", name).Msg("done")
}

// Provider3: extraProviderConnections entry with adminAuth true — same slice-based virtual workspace wiring as default providers, admin cert material.
func (s *KindTestSuite) Test06ExtraProviderAdminKubeconfigProvider3() {
	s.logger.Info().Str("kind_e2e", "Test06ExtraProviderAdminKubeconfigProvider3").Msg("start")
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	sec := s.requireE2EProviderKubeconfigSecret(ctx, e2eAdminKubeconfigProvider3SecretName)
	cfg, err := clientcmd.Load(sec.Data["kubeconfig"])
	s.Require().NoError(err)
	clusterName := cfg.Contexts[cfg.CurrentContext].Cluster
	cluster := cfg.Clusters[clusterName]
	s.Require().NotNil(cluster)
	s.Require().Contains(cluster.Server, "front-proxy", "admin provider kubeconfig should use front-proxy host from operator rewrite")
	s.logger.Info().Str("kind_e2e", "Test06ExtraProviderAdminKubeconfigProvider3").Str("secret", e2eAdminKubeconfigProvider3SecretName).Msg("done")
}

func scopedTryLoadKcpClusterAdminClientCert(s *KindTestSuite, ctx context.Context, sec *corev1.Secret) (namespace string, ok bool) {
	for _, ns := range scopedE2EKcpClusterAdminCertSearchNamespaces {
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      e2eKcpClusterAdminClientCertSecretName,
			Namespace: ns,
		}, sec)
		if err != nil {
			continue
		}
		if sec.Data == nil {
			continue
		}
		if len(sec.Data["ca.crt"]) == 0 || len(sec.Data["tls.crt"]) == 0 || len(sec.Data["tls.key"]) == 0 {
			continue
		}
		return ns, true
	}
	return "", false
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
	sec := &corev1.Secret{}
	var ns string
	var ok bool
	if scopedE2EKcpAdminCertSecretNamespace != "" {
		ns = scopedE2EKcpAdminCertSecretNamespace
		err := s.client.Get(ctx, client.ObjectKey{
			Name:      e2eKcpClusterAdminClientCertSecretName,
			Namespace: ns,
		}, sec)
		if err != nil {
			return nil, err
		}
		ok = sec.Data != nil && len(sec.Data["ca.crt"]) > 0 && len(sec.Data["tls.crt"]) > 0 && len(sec.Data["tls.key"]) > 0
	}
	if !ok {
		ns, ok = scopedTryLoadKcpClusterAdminClientCert(s, ctx, sec)
		if !ok {
			return nil, fmt.Errorf("secret %s not found with TLS data in namespaces %v",
				e2eKcpClusterAdminClientCertSecretName, scopedE2EKcpClusterAdminCertSearchNamespaces)
		}
	}
	ca, crt, key := sec.Data["ca.crt"], sec.Data["tls.crt"], sec.Data["tls.key"]
	if len(ca) == 0 || len(crt) == 0 || len(key) == 0 {
		return nil, fmt.Errorf("secret %s/%s missing ca.crt, tls.crt, or tls.key", ns, e2eKcpClusterAdminClientCertSecretName)
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

// waitForKcpClusterAdminClientCert polls until the infra TLS secret exists (platform-mesh-system or kcp-system).
func (s *KindTestSuite) waitForKcpClusterAdminClientCert(ctx context.Context) {
	sec := &corev1.Secret{}
	s.Eventually(func() bool {
		ns, ok := scopedTryLoadKcpClusterAdminClientCert(s, ctx, sec)
		if !ok {
			s.logger.Warn().
				Str("secret", e2eKcpClusterAdminClientCertSecretName).
				Strs("searchedNamespaces", scopedE2EKcpClusterAdminCertSearchNamespaces).
				Msg("kcp cluster-admin client cert secret not available yet in any candidate namespace")
			return false
		}
		scopedE2EKcpAdminCertSecretNamespace = ns
		s.logger.Info().
			Str("kind_e2e", "Test03ScopedKubeconfigKcpPrereq").
			Str("secret", e2eKcpClusterAdminClientCertSecretName).
			Str("namespace", ns).
			Msg("cluster-admin TLS secret ready")
		return true
	}, 20*time.Minute, 10*time.Second,
		"Secret "+e2eKcpClusterAdminClientCertSecretName+" was not populated in namespaces "+
			strings.Join(scopedE2EKcpClusterAdminCertSearchNamespaces, ", ")+
			" (needed for Test03ScopedKubeconfigKcpPrereq / kcp workspace setup)")
}

// ensureScopedE2EKcpProviderWorkspaces creates root:providers:provider1|provider2 and applies static YAML that models a
// real deployment: APIExport (+ APIResourceSchema + APIExportEndpointSlice) already exists before tests run; tests only
// exercise creating resources from that export via scoped kubeconfigs.
func (s *KindTestSuite) ensureScopedE2EKcpProviderWorkspaces(ctx context.Context) {
	s.logger.Info().Str("kind_e2e", "Test03ScopedKubeconfigKcpPrereq").Msg("seeding root:providers:provider1|provider2 and export YAML")
	emptyTmpl := make(map[string]string)
	rootClient, err := s.kcpClientForWorkspace(ctx, "root")
	s.Require().NoError(err, "kcp client for root")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "workspace-providers.yaml"), rootClient, emptyTmpl),
		"apply workspace-providers.yaml",
	)
	s.logWorkspaceObservedAfterApply(ctx, rootClient, "providers")
	s.waitWorkspaceReady(ctx, rootClient, "providers")
	providersClient, err := s.kcpClientForWorkspace(ctx, "root:providers")
	s.Require().NoError(err, "kcp client for root:providers")
	s.Require().NoError(
		ApplyManifestFromFile(ctx, filepath.Join(e2eKcpProviderWorkspacesYAMLDir, "workspace-provider1-provider2.yaml"), providersClient, emptyTmpl),
		"apply workspace-provider1-provider2.yaml",
	)
	s.logWorkspaceObservedAfterApply(ctx, providersClient, "provider1")
	s.logWorkspaceObservedAfterApply(ctx, providersClient, "provider2")
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
	s.logger.Info().
		Str("kind_e2e", "Test03ScopedKubeconfigKcpPrereq").
		Str("export", e2eKindScopedProviderExportName).
		Msg("provider workspaces and APIExports applied")
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

// logWorkspaceObservedAfterApply helps CI debug: confirms whether the Workspace object is visible right after SSA apply.
func (s *KindTestSuite) logWorkspaceObservedAfterApply(ctx context.Context, cl client.Client, workspaceName string) {
	ws := &unstructured.Unstructured{}
	ws.SetAPIVersion("tenancy.kcp.io/v1alpha1")
	ws.SetKind("Workspace")
	if err := cl.Get(ctx, client.ObjectKey{Name: workspaceName}, ws); err != nil {
		if kerrors.IsNotFound(err) {
			s.logger.Warn().Str("workspace", workspaceName).
				Msg("workspace not visible immediately after apply (apiserver may lag); wait loop will retry")
			return
		}
		s.logger.Warn().Err(err).Str("workspace", workspaceName).
			Msg("could not get workspace right after apply")
		return
	}
	s.logWorkspacePhaseAndConditionMessages(ws, workspaceName, false, "workspace observed after apply")
}

// logWorkspacePhaseAndConditionMessages logs status.phase and each condition's message field in full (no truncation).
func (s *KindTestSuite) logWorkspacePhaseAndConditionMessages(ws *unstructured.Unstructured, workspaceName string, warn bool, msg string) {
	phase, _, _ := unstructured.NestedString(ws.Object, "status", "phase")
	var messages []string
	conditions, ok, _ := unstructured.NestedSlice(ws.Object, "status", "conditions")
	if ok {
		for _, raw := range conditions {
			cm, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			condType, _, _ := unstructured.NestedString(cm, "type")
			m, _, _ := unstructured.NestedString(cm, "message")
			reason, _, _ := unstructured.NestedString(cm, "reason")
			switch {
			case m != "":
				messages = append(messages, m)
			case reason != "":
				// kcp often omits message on True conditions; False frequently sets reason only.
				if condType != "" {
					messages = append(messages, condType+": "+reason)
				} else {
					messages = append(messages, reason)
				}
			}
		}
	}
	evt := s.logger.Info()
	if warn {
		evt = s.logger.Warn()
	}
	evt = evt.Str("workspace", workspaceName)
	if phase != "" {
		evt = evt.Str("phase", phase)
	}
	if len(messages) > 0 {
		evt = evt.Str("condition_messages", strings.Join(messages, "\n---\n"))
	}
	evt.Msg(msg)
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
			s.logWorkspacePhaseAndConditionMessages(ws, workspaceName, true, "workspace exists but not Ready yet")
			return false
		}
		return true
	}, 3*time.Minute, 10*time.Second, "workspace "+workspaceName+" did not become ready")
}

// Same Ready gate as Test01ResourceReady; scoped test only (not shared with other test files).
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
