package e2e

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/rbacpresets"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Kind e2e tests for provider RBAC presets (PlatformMesh.spec.kcp.extraProviderConnections[].providerRBACPreset).
// Each case merges a test-only preset from yaml/kcp-preset-fixtures into the suite's rbacpresets.Loader.FS, patches one
// extra provider connection, waits for the operator-written kubeconfig Secret, then asserts cluster.server; it also checks
// ServiceAccount / ClusterRole / ClusterRoleBinding in the expected workspace, and where useful kubectl auth can-i.
// TestX_* runs after TestScoped* lexicographically.
const (
	// Shared by preset tests: on-disk fixtures and front-proxy host used in expected cluster.server values.
	e2ePresetFixtureDir     = "../../../test/e2e/kind/yaml/kcp-preset-fixtures"
	e2ePresetFrontProxyBase = "https://frontproxy-front-proxy.platform-mesh-system:8443"

	// TestX_Preset01KubeconfigPrereq — seeds WorkspaceType so Preset05 can read status.virtualWorkspaces[0].URL.
	e2ePresetWorkspaceTypeName = "e2e-preset-target"
	e2ePresetWorkspaceTypeYAML = "e2e-wtvw-workspacetype.yaml"

	// TestX_Preset02KubeconfigWorkspaceCluster — providerRBACPreset e2e-workspace-cluster (serverTarget workspaceCluster).
	e2ePresetInitAgentSecretName     = "kind-e2e-preset-init-agent-kubeconfig"
	e2ePresetWorkspaceClusterPreset  = "e2e-workspace-cluster"
	e2ePresetWorkspaceClusterFixture = "e2e-workspace-cluster.yaml"

	// TestX_Preset03KubeconfigRawPath — providerRBACPreset e2e-raw-path (serverTarget rawPath).
	e2ePresetMarketplaceSecretName = "kind-e2e-preset-marketplace-kubeconfig"
	e2ePresetRawPathPreset         = "e2e-raw-path"
	e2ePresetRawPathFixture        = "e2e-raw-path.yaml"

	// TestX_Preset04KubeconfigPathRawPath — providerRBACPreset e2e-path-raw-path (serverTarget pathRawPath).
	e2ePresetPortalSecretName   = "kind-e2e-preset-portal-kubeconfig"
	e2ePresetPathRawPathPreset  = "e2e-path-raw-path"
	e2ePresetPathRawPathFixture = "e2e-path-raw-path.yaml"

	// TestX_Preset05KubeconfigWorkspaceTypeVW — providerRBACPreset e2e-wtvw (workspaceTypeVirtualWorkspace; needs Preset01).
	e2ePresetWTVWSecretName = "kind-e2e-preset-wtvw-kubeconfig"
	e2ePresetWTVWPreset     = "e2e-wtvw"
	e2ePresetWTVWFixture    = "e2e-wtvw.yaml"
)

// e2ePresetFixtureFiles maps preset name to fixture basename (ProviderRBACPreset YAML only).
var e2ePresetFixtureFiles = map[string]string{
	e2ePresetWorkspaceClusterPreset: e2ePresetWorkspaceClusterFixture,
	e2ePresetRawPathPreset:          e2ePresetRawPathFixture,
	e2ePresetPathRawPathPreset:      e2ePresetPathRawPathFixture,
	e2ePresetWTVWPreset:             e2ePresetWTVWFixture,
}

// buildE2EPresetOverlayFS loads all Kind e2e preset fixtures into providers/<name>.yaml for the in-process operator.
// Required at operator start because the Kind cluster is reused and PlatformMesh may still reference e2e presets from a prior run.
func buildE2EPresetOverlayFS() (fstest.MapFS, error) {
	overlay := fstest.MapFS{}
	for presetName, fixtureBasename := range e2ePresetFixtureFiles {
		path := filepath.Join(e2ePresetFixtureDir, fixtureBasename)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read preset fixture %s: %w", path, err)
		}
		mapKey := filepath.ToSlash(filepath.Join("providers", presetName+".yaml"))
		overlay[mapKey] = &fstest.MapFile{Data: data}
	}
	return overlay, nil
}

// OverlayPresetLoaderWithFixtureFile merges fixtureBasename from kcp-preset-fixtures into the Kind suite's preset loader at
// providers/<presetName>.yaml (must match Loader.LoadPreset and PlatformMesh providerRBACPreset).
//
// Layers are intentionally left in place across tests: each case patches PlatformMesh with an extra preset connection that
// remains for the lifetime of the suite. Restoring FS on teardown would make the reconciler retry LoadPreset against the CR
// and fail (e.g. "e2e-workspace-cluster" missing after Preset02 cleanup while Preset03 runs).
func (s *KindTestSuite) OverlayPresetLoaderWithFixtureFile(presetName, fixtureBasename string) {
	s.Require().NotNil(s.presetLoader, "presetLoader is set when the in-process operator starts")
	prevFS := s.presetLoader.FS
	path := filepath.Join(e2ePresetFixtureDir, fixtureBasename)
	data, err := os.ReadFile(path)
	s.Require().NoError(err, "read preset fixture %s", path)
	mapKey := filepath.ToSlash(filepath.Join("providers", presetName+".yaml"))
	s.presetLoader.FS = rbacpresets.MergePresetFS(prevFS, fstest.MapFS{
		mapKey: &fstest.MapFile{Data: data},
	})
}

// TestX_Preset01KubeconfigPrereq seeds kcp with a WorkspaceType used only for the workspaceTypeVirtualWorkspace preset case:
// it applies e2e-wtvw-workspacetype.yaml in workspace root and blocks until .status.virtualWorkspaces[0].URL is set,
// which Preset05 needs to build the expected kubeconfig server URL.
func (s *KindTestSuite) TestX_Preset01KubeconfigPrereq() {
	s.logger.Info().Str("kind_e2e", "TestX_Preset01KubeconfigPrereq").Msg("start")
	ctx := context.Background()
	rootClient, err := s.kcpClientForWorkspace(ctx, "root")
	s.Require().NoError(err, "kcp client for root (preset WT fixture)")
	path := filepath.Join(e2ePresetFixtureDir, e2ePresetWorkspaceTypeYAML)
	s.Require().NoError(
		ApplyManifestFromFile(ctx, path, rootClient, make(map[string]string)),
		"apply preset WorkspaceType fixture",
	)
	_ = s.AwaitWorkspaceTypeVirtualWorkspaceURL(ctx, "root", e2ePresetWorkspaceTypeName)
	s.logger.Info().Str("kind_e2e", "TestX_Preset01KubeconfigPrereq").Msg("done")
}

// TestX_Preset02KubeconfigWorkspaceCluster covers providerRBACPreset "e2e-workspace-cluster" (fixture YAML, serverTarget workspaceCluster):
// scoped kubeconfig for root:platform-mesh-system, server is the workspace cluster URL on the front proxy;
// checks SA/RBAC objects in that workspace and kubectl auth can-i list inittargets.initialization.kcp.io.
func (s *KindTestSuite) TestX_Preset02KubeconfigWorkspaceCluster() {
	s.logger.Info().Str("kind_e2e", "TestX_Preset02KubeconfigWorkspaceCluster").Msg("start")
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)
	s.OverlayPresetLoaderWithFixtureFile(e2ePresetWorkspaceClusterPreset, e2ePresetWorkspaceClusterFixture)
	s.SetPlatformMeshProviderConnectionBySecretName(ctx, corev1alpha1.ProviderConnection{
		Path:               "root:platform-mesh-system",
		Secret:             e2ePresetInitAgentSecretName,
		ProviderRBACPreset: ptr.To(e2ePresetWorkspaceClusterPreset),
		AdminAuth:          ptr.To(false),
	})
	s.AwaitProviderKubeconfigSecret(ctx, e2ePresetInitAgentSecretName)
	sec := s.ReadProviderKubeconfigSecretOrFail(ctx, e2ePresetInitAgentSecretName)
	kcfg := sec.Data["kubeconfig"]
	wantServer := e2ePresetFrontProxyBase + "/clusters/root:platform-mesh-system"
	s.Require().Equal(wantServer, CurrentContextClusterServer(s.T(), kcfg), "e2e-workspace-cluster preset server URL")

	saName := "platform-mesh-provider-" + e2ePresetInitAgentSecretName
	s.RequireProviderIdentityRBACInWorkspace(ctx, "root:platform-mesh-system", saName)

	ok, err := runKubectlAuthCanI(kcfg, "list", "inittargets.initialization.kcp.io")
	s.Require().NoError(err)
	s.Require().True(ok, "scoped preset identity must list inittargets")
	s.logger.Info().Str("kind_e2e", "TestX_Preset02KubeconfigWorkspaceCluster").Msg("done")
}

// TestX_Preset03KubeconfigRawPath covers providerRBACPreset "e2e-raw-path" (fixture YAML, serverTarget rawPath):
// kubeconfig server is /services/marketplace on the front proxy (no connection Path); verifies SA and RBAC in root:platform-mesh-system.
func (s *KindTestSuite) TestX_Preset03KubeconfigRawPath() {
	s.logger.Info().Str("kind_e2e", "TestX_Preset03KubeconfigRawPath").Msg("start")
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)
	s.OverlayPresetLoaderWithFixtureFile(e2ePresetRawPathPreset, e2ePresetRawPathFixture)
	s.SetPlatformMeshProviderConnectionBySecretName(ctx, corev1alpha1.ProviderConnection{
		Secret:             e2ePresetMarketplaceSecretName,
		ProviderRBACPreset: ptr.To(e2ePresetRawPathPreset),
		AdminAuth:          ptr.To(false),
	})
	s.AwaitProviderKubeconfigSecret(ctx, e2ePresetMarketplaceSecretName)
	sec := s.ReadProviderKubeconfigSecretOrFail(ctx, e2ePresetMarketplaceSecretName)
	kcfg := sec.Data["kubeconfig"]
	wantServer, err := url.JoinPath(e2ePresetFrontProxyBase, "/services/marketplace")
	s.Require().NoError(err)
	s.Require().Equal(wantServer, CurrentContextClusterServer(s.T(), kcfg), "e2e-raw-path preset server URL")

	saName := "platform-mesh-provider-" + e2ePresetMarketplaceSecretName
	s.RequireProviderIdentityRBACInWorkspace(ctx, "root:platform-mesh-system", saName)
	s.logger.Info().Str("kind_e2e", "TestX_Preset03KubeconfigRawPath").Msg("done")
}

// TestX_Preset04KubeconfigPathRawPath covers providerRBACPreset "e2e-path-raw-path" (fixture YAML, serverTarget pathRawPath): connection Path
// root:orgs, server ends with /services/contentconfigurations; checks SA/RBAC in root:orgs; prefers kubectl auth can-i
// list contentconfigurations.core.platform-mesh.io, otherwise asserts the ClusterRole rules if that API is unavailable.
func (s *KindTestSuite) TestX_Preset04KubeconfigPathRawPath() {
	s.logger.Info().Str("kind_e2e", "TestX_Preset04KubeconfigPathRawPath").Msg("start")
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)
	s.OverlayPresetLoaderWithFixtureFile(e2ePresetPathRawPathPreset, e2ePresetPathRawPathFixture)
	s.SetPlatformMeshProviderConnectionBySecretName(ctx, corev1alpha1.ProviderConnection{
		Path:               "root:orgs",
		Secret:             e2ePresetPortalSecretName,
		ProviderRBACPreset: ptr.To(e2ePresetPathRawPathPreset),
		AdminAuth:          ptr.To(false),
	})
	s.AwaitProviderKubeconfigSecret(ctx, e2ePresetPortalSecretName)
	sec := s.ReadProviderKubeconfigSecretOrFail(ctx, e2ePresetPortalSecretName)
	kcfg := sec.Data["kubeconfig"]
	wantServer, err := url.JoinPath(e2ePresetFrontProxyBase, "/services/contentconfigurations")
	s.Require().NoError(err)
	s.Require().Equal(wantServer, CurrentContextClusterServer(s.T(), kcfg), "e2e-path-raw-path preset server URL")

	saName := "platform-mesh-provider-" + e2ePresetPortalSecretName
	s.RequireProviderIdentityRBACInWorkspace(ctx, "root:orgs", saName)

	ok, err := runKubectlAuthCanI(kcfg, "list", "contentconfigurations.core.platform-mesh.io")
	if err == nil && ok {
		s.logger.Info().Msg("e2e-path-raw-path preset: auth can-i list contentconfigurations ok")
	} else {
		s.logger.Warn().Err(err).Bool("allowed", ok).Msg("e2e-path-raw-path preset: auth can-i skipped or failed; asserting ClusterRole rules")
		cl, err2 := s.kcpClientForWorkspace(ctx, "root:orgs")
		s.Require().NoError(err2)
		var cr rbacv1.ClusterRole
		s.Require().NoError(cl.Get(ctx, client.ObjectKey{Name: saName}, &cr))
		var found bool
		for _, r := range cr.Rules {
			for _, g := range r.APIGroups {
				if g == "core.platform-mesh.io" {
					for _, res := range r.Resources {
						if res == "contentconfigurations" {
							found = true
							break
						}
					}
				}
			}
		}
		s.Require().True(found, "e2e-path-raw-path ClusterRole must include core.platform-mesh.io/contentconfigurations")
	}
	s.logger.Info().Str("kind_e2e", "TestX_Preset04KubeconfigPathRawPath").Msg("done")
}

// TestX_Preset05KubeconfigWorkspaceTypeVW covers workspaceTypeVirtualWorkspace using fixture preset "e2e-wtvw"
// (fixture merged into the suite preset loader; layers persist across Kind preset tests alongside PlatformMesh CR updates).
// Requires Preset01: connection Path root, kubeconfig server must match front-proxy host + path from the WorkspaceType VW URL;
// asserts SA/RBAC in root.
func (s *KindTestSuite) TestX_Preset05KubeconfigWorkspaceTypeVW() {
	s.logger.Info().Str("kind_e2e", "TestX_Preset05KubeconfigWorkspaceTypeVW").Msg("start")
	ctx := context.TODO()
	s.scopedWaitPlatformMeshReady(ctx)

	s.OverlayPresetLoaderWithFixtureFile(e2ePresetWTVWPreset, e2ePresetWTVWFixture)

	vwURL := s.AwaitWorkspaceTypeVirtualWorkspaceURL(ctx, "root", e2ePresetWorkspaceTypeName)
	wantServer, err := KubeconfigServerURLForFrontProxyAndVWPath(vwURL)
	s.Require().NoError(err)

	s.SetPlatformMeshProviderConnectionBySecretName(ctx, corev1alpha1.ProviderConnection{
		Path:               "root",
		Secret:             e2ePresetWTVWSecretName,
		ProviderRBACPreset: ptr.To(e2ePresetWTVWPreset),
		AdminAuth:          ptr.To(false),
	})
	s.AwaitProviderKubeconfigSecret(ctx, e2ePresetWTVWSecretName)
	sec := s.ReadProviderKubeconfigSecretOrFail(ctx, e2ePresetWTVWSecretName)
	kcfg := sec.Data["kubeconfig"]
	s.Require().Equal(wantServer, CurrentContextClusterServer(s.T(), kcfg), "workspaceTypeVirtualWorkspace preset server URL")

	saName := "platform-mesh-provider-" + e2ePresetWTVWSecretName
	s.RequireProviderIdentityRBACInWorkspace(ctx, "root", saName)
	s.logger.Info().Str("kind_e2e", "TestX_Preset05KubeconfigWorkspaceTypeVW").Msg("done")
}

func CurrentContextClusterServer(t requireInterface, kubeconfigBytes []byte) string {
	t.Helper()
	cfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		t.Fatalf("parse kubeconfig: %v", err)
		return ""
	}
	cur := cfg.Contexts[cfg.CurrentContext]
	if cur == nil {
		t.Fatalf("missing context %q", cfg.CurrentContext)
		return ""
	}
	cluster := cfg.Clusters[cur.Cluster]
	if cluster == nil {
		t.Fatalf("missing cluster %q", cur.Cluster)
		return ""
	}
	return cluster.Server
}

type requireInterface interface {
	Helper()
	Fatalf(format string, args ...interface{})
}

func (s *KindTestSuite) SetPlatformMeshProviderConnectionBySecretName(ctx context.Context, d corev1alpha1.ProviderConnection) {
	pm := &corev1alpha1.PlatformMesh{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      e2ePlatformMeshName,
		Namespace: e2ePlatformMeshNamespace,
	}, pm)
	s.Require().NoError(err, "get PlatformMesh for preset e2e provider connection")

	currentBySecret := make(map[string]int, len(pm.Spec.Kcp.ExtraProviderConnections))
	for i, pc := range pm.Spec.Kcp.ExtraProviderConnections {
		currentBySecret[pc.Secret] = i
	}

	if idx, ok := currentBySecret[d.Secret]; ok {
		if providerConnectionEquivalent(pm.Spec.Kcp.ExtraProviderConnections[idx], d) {
			s.logger.Info().Str("secret", d.Secret).Msg("preset e2e: provider connection already desired")
			return
		}
		pm.Spec.Kcp.ExtraProviderConnections[idx] = d
	} else {
		pm.Spec.Kcp.ExtraProviderConnections = append(pm.Spec.Kcp.ExtraProviderConnections, d)
	}

	s.Require().NoError(s.client.Update(ctx, pm), "update PlatformMesh preset e2e extraProviderConnections")
	s.logger.Info().Str("secret", d.Secret).Msg("preset e2e: provider connection updated")
}

func (s *KindTestSuite) AwaitProviderKubeconfigSecret(ctx context.Context, secretName string) {
	s.Eventually(func() bool {
		sec := &corev1.Secret{}
		if err := s.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: e2ePlatformMeshNamespace}, sec); err != nil {
			s.logger.Info().Str("secret", secretName).Msg("preset e2e: provider secret not yet present")
			return false
		}
		if len(sec.Data["kubeconfig"]) == 0 {
			s.logger.Info().Str("secret", secretName).Msg("preset e2e: provider secret kubeconfig empty")
			return false
		}
		return true
	}, 6*time.Minute, 10*time.Second, "preset provider secret %s/%s not ready", e2ePlatformMeshNamespace, secretName)
}

func (s *KindTestSuite) ReadProviderKubeconfigSecretOrFail(ctx context.Context, secretName string) *corev1.Secret {
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{Name: secretName, Namespace: e2ePlatformMeshNamespace}, sec)
	s.Require().NoError(err, "get preset provider secret %s/%s", e2ePlatformMeshNamespace, secretName)
	s.Require().NotEmpty(sec.Data["kubeconfig"])
	return sec
}

func (s *KindTestSuite) RequireProviderIdentityRBACInWorkspace(ctx context.Context, workspacePath, saName string) {
	cl, err := s.kcpClientForWorkspace(ctx, workspacePath)
	s.Require().NoError(err, "kcp client for workspace %s", workspacePath)
	var sa corev1.ServiceAccount
	s.Require().NoError(cl.Get(ctx, client.ObjectKey{Name: saName, Namespace: "default"}, &sa))
	var cr rbacv1.ClusterRole
	s.Require().NoError(cl.Get(ctx, client.ObjectKey{Name: saName}, &cr))
	var crb rbacv1.ClusterRoleBinding
	s.Require().NoError(cl.Get(ctx, client.ObjectKey{Name: saName}, &crb))
}

func (s *KindTestSuite) AwaitWorkspaceTypeVirtualWorkspaceURL(ctx context.Context, workspacePath, wtName string) string {
	cl, err := s.kcpClientForWorkspace(ctx, workspacePath)
	s.Require().NoError(err)
	var got string
	s.Eventually(func() bool {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("tenancy.kcp.io/v1alpha1")
		u.SetKind("WorkspaceType")
		if err := cl.Get(ctx, client.ObjectKey{Name: wtName}, u); err != nil {
			return false
		}
		vws, found, _ := unstructured.NestedSlice(u.Object, "status", "virtualWorkspaces")
		if !found || len(vws) == 0 {
			return false
		}
		first, ok := vws[0].(map[string]interface{})
		if !ok {
			return false
		}
		raw, ok, _ := unstructured.NestedString(first, "url")
		if !ok || strings.TrimSpace(raw) == "" {
			return false
		}
		got = raw
		return true
	}, 6*time.Minute, 10*time.Second, "WorkspaceType %q in %s missing status.virtualWorkspaces URL", wtName, workspacePath)
	return got
}

func KubeconfigServerURLForFrontProxyAndVWPath(vwStatusURL string) (string, error) {
	u, err := url.Parse(vwStatusURL)
	if err != nil {
		return "", err
	}
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("virtual workspace URL %q has no path", vwStatusURL)
	}
	out, err := url.JoinPath(e2ePresetFrontProxyBase, u.Path)
	if err != nil {
		return "", err
	}
	out = strings.TrimSuffix(out, "/")
	if u.RawQuery != "" {
		return out + "?" + u.RawQuery, nil
	}
	return out, nil
}
