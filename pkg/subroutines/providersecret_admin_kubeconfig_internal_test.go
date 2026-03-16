package subroutines

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_buildServerURLFromAdminKubeconfig_prefersWorkspacePath(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	adminCfg := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {Server: "https://root-kcp.kcp-system:6443/clusters/root"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default-auth": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default-context": {Cluster: "default", AuthInfo: "default-auth"},
		},
		CurrentContext: "default-context",
	}
	adminKubeconfigData, err := clientcmd.Write(*adminCfg)
	require.NoError(t, err)

	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AdminKubeconfigSecretName,
			Namespace: "kcp-system",
		},
		Data: map[string][]byte{"kubeconfig": adminKubeconfigData},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(adminSecret).Build()
	got, err := buildServerURLFromAdminKubeconfig(
		ctx,
		cl,
		"kcp-system",
		"https://frontproxy.platform-mesh-system:8443",
		"root:platform-mesh-system",
	)
	require.NoError(t, err)
	require.Equal(t, "https://frontproxy.platform-mesh-system:8443/clusters/root:platform-mesh-system", got)
}

func Test_writeProviderSecretFromAdminKubeconfig_prefersRawPathAndFrontProxyCA(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	srcCfg := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {
				Server:                   "https://root-kcp.kcp-system:6443/clusters/root",
				CertificateAuthorityData: []byte("old-ca"),
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default-auth": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default-context": {Cluster: "default", AuthInfo: "default-auth"},
		},
		CurrentContext: "default-context",
	}
	srcData, err := clientcmd.Write(*srcCfg)
	require.NoError(t, err)

	err = writeProviderSecretFromAdminKubeconfig(
		ctx,
		cl,
		srcData,
		"https://frontproxy.platform-mesh-system:8443",
		"root:orgs",
		"/services/contentconfigurations",
		[]byte("frontproxy-ca"),
		"portal-kubeconfig",
		"platform-mesh-system",
	)
	require.NoError(t, err)

	secret := &corev1.Secret{}
	err = cl.Get(ctx, client.ObjectKey{Name: "portal-kubeconfig", Namespace: "platform-mesh-system"}, secret)
	require.NoError(t, err)
	require.NotEmpty(t, secret.Data["kubeconfig"])

	gotCfg, err := clientcmd.Load(secret.Data["kubeconfig"])
	require.NoError(t, err)
	cluster := gotCfg.Clusters["default"]
	require.NotNil(t, cluster)
	require.Equal(t, "https://frontproxy.platform-mesh-system:8443/services/contentconfigurations", cluster.Server)
	require.Equal(t, []byte("frontproxy-ca"), cluster.CertificateAuthorityData)
	require.Equal(t, "", cluster.CertificateAuthority)
	require.False(t, cluster.InsecureSkipTLSVerify)
}

func Test_writeProviderSecretFromAdminKubeconfig_malformedServerFallsBackToHostPort(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	srcCfg := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {Server: "://bad-url"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default-auth": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default-context": {Cluster: "default", AuthInfo: "default-auth"},
		},
		CurrentContext: "default-context",
	}
	srcData, err := clientcmd.Write(*srcCfg)
	require.NoError(t, err)

	err = writeProviderSecretFromAdminKubeconfig(
		ctx,
		cl,
		srcData,
		"https://frontproxy.platform-mesh-system:8443",
		"",
		"",
		nil,
		"generic-kubeconfig",
		"platform-mesh-system",
	)
	require.NoError(t, err)

	secret := &corev1.Secret{}
	err = cl.Get(ctx, client.ObjectKey{Name: "generic-kubeconfig", Namespace: "platform-mesh-system"}, secret)
	require.NoError(t, err)

	gotCfg, err := clientcmd.Load(secret.Data["kubeconfig"])
	require.NoError(t, err)
	require.Equal(t, "https://frontproxy.platform-mesh-system:8443", gotCfg.Clusters["default"].Server)
}
