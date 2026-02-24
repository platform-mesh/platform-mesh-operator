package subroutines_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

func TestBuildScopedKubeconfig(t *testing.T) {
	hostURL := "https://frontproxy-front-proxy.platform-mesh-system:6443/clusters/root:platform-mesh-system"
	token := "test-token"
	caData := []byte("ca-data")

	cfg := subroutines.BuildScopedKubeconfig(hostURL, token, caData)
	require.NotNil(t, cfg)
	require.Equal(t, "default-context", cfg.CurrentContext)
	require.Contains(t, cfg.Clusters, "default-cluster")
	assert.Equal(t, hostURL, cfg.Clusters["default-cluster"].Server)
	assert.Equal(t, caData, cfg.Clusters["default-cluster"].CertificateAuthorityData)
	require.Contains(t, cfg.AuthInfos, "default-auth")
	assert.Equal(t, token, cfg.AuthInfos["default-auth"].Token)
	require.Contains(t, cfg.Contexts, "default-context")
	assert.Equal(t, "default-cluster", cfg.Contexts["default-context"].Cluster)
	assert.Equal(t, "default-auth", cfg.Contexts["default-context"].AuthInfo)
}

func TestBuildScopedKubeconfig_roundtrip(t *testing.T) {
	hostURL := "https://kcp.example.com/clusters/root:orgs"
	token := "eyJhbGc..."
	caData := []byte(" PEM-CA-DATA ")

	cfg := subroutines.BuildScopedKubeconfig(hostURL, token, caData)
	bytes, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	loaded, err := clientcmd.Load(bytes)
	require.NoError(t, err)
	assert.Equal(t, hostURL, loaded.Clusters["default-cluster"].Server)
	assert.Equal(t, token, loaded.AuthInfos["default-auth"].Token)
}

func TestBuildHostURLForScoped(t *testing.T) {
	tests := []struct {
		name     string
		hostPort string
		path     string
		wantURL  string
		wantErr  bool
	}{
		{
			name:     "normal",
			hostPort: "https://frontproxy.platform-mesh-system:6443",
			path:     "root:platform-mesh-system",
			wantURL:  "https://frontproxy.platform-mesh-system:6443/clusters/root:platform-mesh-system",
			wantErr:  false,
		},
		{
			name:     "with trailing slash hostPort",
			hostPort: "https://kcp.example.com:8443/",
			path:     "root:orgs",
			wantURL:  "https://kcp.example.com:8443/clusters/root:orgs",
			wantErr:  false,
		},
		{
			name:     "empty path",
			hostPort: "https://kcp.example.com",
			path:     "",
			wantURL:  "https://kcp.example.com/clusters",
			wantErr:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := subroutines.BuildHostURLForScoped(tt.hostPort, tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, got)
		})
	}
}

func TestBuildHostURLForScoped_invalidHostPort(t *testing.T) {
	_, err := subroutines.BuildHostURLForScoped("://missing-scheme", "root:test")
	assert.Error(t, err)
}

func TestWriteScopedKubeconfigToSecret_requiresAPIExportName(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	cfg := &rest.Config{Host: "https://kcp.example.com", TLSClientConfig: rest.TLSClientConfig{}}
	spec := subroutines.ProviderConnectionSpec{
		Path:          "root:platform-mesh-system",
		Secret:        "test-secret",
		APIExportName: "",
	}
	err := subroutines.WriteScopedKubeconfigToSecret(ctx, mockClient, cfg, spec, "https://frontproxy.example.com:6443", "platform-mesh-system")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APIExportName")
}

func TestWriteScopedKubeconfigToSecret_requiresPath(t *testing.T) {
	ctx := context.Background()
	mockClient := new(mocks.Client)
	cfg := &rest.Config{Host: "https://kcp.example.com", TLSClientConfig: rest.TLSClientConfig{}}
	spec := subroutines.ProviderConnectionSpec{
		Path:          "",
		Secret:        "test-secret",
		APIExportName: "core.platform-mesh.io",
	}
	err := subroutines.WriteScopedKubeconfigToSecret(ctx, mockClient, cfg, spec, "https://frontproxy.example.com:6443", "platform-mesh-system")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Path")
}
