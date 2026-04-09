package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	kindE2EAdminProviderKubeconfigSecretName = "kind-e2e-admin-kubeconfig"
	kindE2EAdminProviderWorkspacePath        = "root:platform-mesh-system"
)

// It registers an extra AdminAuth ProviderConnection so the operator
// materializes a provider kubeconfig from kubeconfig-kcp-admin (admin path), then verifies TLS + API access.
func (s *KindTestSuite) Test03AdminKubeconfigSelfContained() {
	s.logger.Info().Str("kind_e2e", "Test03AdminKubeconfigSelfContained").Msg("start")
	ctx := context.Background()
	s.runAdminKubeconfigSelfContainedE2E(ctx)
	s.logger.Info().Str("kind_e2e", "Test03AdminKubeconfigSelfContained").Msg("done")
}

func (s *KindTestSuite) runAdminKubeconfigSelfContainedE2E(ctx context.Context) {
	s.adminE2EPatchExtraProviderConnectionForAdminKubeconfig(ctx)
	s.adminE2EWaitAdminProviderKubeconfigSecret(ctx)

	sec := s.adminE2ERequireAdminProviderSecret(ctx)
	kcfg := sec.Data["kubeconfig"]

	var err error
	kcfg, err = normalizeAdminProviderKubeconfigForLocalRun(kcfg)
	s.Require().NoError(err, "normalize admin provider kubeconfig for host must succeed")

	dyn, err := dynamicClientForKubeconfig(kcfg)
	s.Require().NoError(err, "build dynamic client from admin provider kubeconfig must succeed")
	s.Require().NoError(
		func() error {
			gvr := schema.GroupVersionResource{Group: "core.platform-mesh.io", Version: "v1alpha1", Resource: "accounts"}
			_, err := dyn.Resource(gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
			return err
		}(),
		"list accounts.core.platform-mesh.io with admin provider kubeconfig must succeed",
	)
}

func (s *KindTestSuite) adminE2EPatchExtraProviderConnectionForAdminKubeconfig(ctx context.Context) {
	pm := &corev1alpha1.PlatformMesh{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      e2ePlatformMeshName,
		Namespace: e2ePlatformMeshNamespace,
	}, pm)
	s.Require().NoError(err, "get PlatformMesh for admin e2e provider connection")

	desired := corev1alpha1.ProviderConnection{
		Path:              kindE2EAdminProviderWorkspacePath,
		Secret:            kindE2EAdminProviderKubeconfigSecretName,
		EndpointSliceName: ptr.To("core.platform-mesh.io"),
		AdminAuth:         ptr.To(true),
	}

	currentBySecret := make(map[string]int, len(pm.Spec.Kcp.ExtraProviderConnections))
	for i, pc := range pm.Spec.Kcp.ExtraProviderConnections {
		currentBySecret[pc.Secret] = i
	}

	if idx, ok := currentBySecret[kindE2EAdminProviderKubeconfigSecretName]; ok {
		if providerConnectionEquivalent(pm.Spec.Kcp.ExtraProviderConnections[idx], desired) {
			s.logger.Info().Msg("admin e2e: provider connection already present")
			return
		}
		pm.Spec.Kcp.ExtraProviderConnections[idx] = desired
	} else {
		pm.Spec.Kcp.ExtraProviderConnections = append(pm.Spec.Kcp.ExtraProviderConnections, desired)
	}

	s.Require().NoError(s.client.Update(ctx, pm), "update PlatformMesh admin e2e extraProviderConnections")
	s.logger.Info().Str("secret", kindE2EAdminProviderKubeconfigSecretName).Msg("admin e2e: provider connection updated")
}

func (s *KindTestSuite) adminE2EWaitAdminProviderKubeconfigSecret(ctx context.Context) {
	name := kindE2EAdminProviderKubeconfigSecretName
	s.Eventually(func() bool {
		sec := &corev1.Secret{}
		if err := s.client.Get(ctx, client.ObjectKey{Name: name, Namespace: e2ePlatformMeshNamespace}, sec); err != nil {
			s.logger.Info().Str("secret", name).Msg("admin e2e: operator provider secret not yet present")
			return false
		}
		if len(sec.Data["kubeconfig"]) == 0 {
			s.logger.Info().Str("secret", name).Msg("admin e2e: provider secret kubeconfig empty")
			return false
		}
		return true
	}, 6*time.Minute, 10*time.Second, "admin e2e provider secret %s/%s not ready", e2ePlatformMeshNamespace, name)
}

func (s *KindTestSuite) adminE2ERequireAdminProviderSecret(ctx context.Context) *corev1.Secret {
	sec := &corev1.Secret{}
	err := s.client.Get(ctx, client.ObjectKey{
		Name:      kindE2EAdminProviderKubeconfigSecretName,
		Namespace: e2ePlatformMeshNamespace,
	}, sec)
	s.Require().NoError(err, "provider kubeconfig secret %s/%s", e2ePlatformMeshNamespace, kindE2EAdminProviderKubeconfigSecretName)
	s.Require().NotEmpty(sec.Data["kubeconfig"], "secret %s must contain kubeconfig", kindE2EAdminProviderKubeconfigSecretName)
	return sec
}

func normalizeAdminProviderKubeconfigForLocalRun(kubeconfigBytes []byte) ([]byte, error) {
	cfg, err := clientcmd.Load(kubeconfigBytes)
	if err != nil {
		return nil, err
	}
	cur := cfg.Contexts[cfg.CurrentContext]
	cluster := cfg.Clusters[cur.Cluster]
	server := cluster.Server
	server = strings.Replace(server, "frontproxy-front-proxy.platform-mesh-system:6443", "localhost:8443", 1)
	if strings.Contains(server, "/services/apiexport/") {
		server = fmt.Sprintf("https://localhost:8443/clusters/%s", kindE2EAdminProviderWorkspacePath)
	}
	cluster.Server = server
	return clientcmd.Write(*cfg)
}
