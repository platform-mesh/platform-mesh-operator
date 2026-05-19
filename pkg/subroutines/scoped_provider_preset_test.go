package subroutines

import (
	"context"
	"fmt"
	"testing"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/rbacpresets"
	"github.com/stretchr/testify/require"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type presetTestKcpHelper struct {
	clients map[string]client.Client
}

func (h presetTestKcpHelper) NewKcpClient(_ *rest.Config, workspacePath string) (client.Client, error) {
	if cl, ok := h.clients[workspacePath]; ok {
		return cl, nil
	}
	return nil, fmt.Errorf("unexpected workspace %s", workspacePath)
}

type tokenClient struct {
	client.Client
	token string
}

func (c tokenClient) SubResource(subResource string) client.SubResourceClient {
	if subResource == "token" {
		return tokenSubResource{token: c.token}
	}
	return c.Client.SubResource(subResource)
}

type tokenSubResource struct {
	token string
}

func (s tokenSubResource) Get(context.Context, client.Object, client.Object, ...client.SubResourceGetOption) error {
	return nil
}

func (s tokenSubResource) Create(_ context.Context, _ client.Object, subResource client.Object, _ ...client.SubResourceCreateOption) error {
	tokenRequest, ok := subResource.(*authv1.TokenRequest)
	if !ok {
		return fmt.Errorf("unexpected token subresource type %T", subResource)
	}
	tokenRequest.Status.Token = s.token
	return nil
}

func (s tokenSubResource) Update(context.Context, client.Object, ...client.SubResourceUpdateOption) error {
	return nil
}

func (s tokenSubResource) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return nil
}

func (s tokenSubResource) Apply(context.Context, runtime.ApplyConfiguration, ...client.SubResourceApplyOption) error {
	return nil
}

func TestBuildPresetServerURL(t *testing.T) {
	t.Parallel()

	operatorCfg := presetTestOperatorConfig()
	instance := &corev1alpha1.PlatformMesh{
		Spec: corev1alpha1.PlatformMeshSpec{
			Exposure: &corev1alpha1.ExposureConfig{
				BaseDomain: "example.test",
				Port:       443,
			},
		},
	}
	cfg := &rest.Config{Host: "https://root.kcp.test", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}

	workspaceTypeClient := fake.NewClientBuilder().
		WithScheme(presetTestScheme(t)).
		WithObjects(&kcptenancyv1alpha.WorkspaceType{
			ObjectMeta: metav1.ObjectMeta{Name: "org"},
			Status: kcptenancyv1alpha.WorkspaceTypeStatus{
				VirtualWorkspaces: []kcptenancyv1alpha.VirtualWorkspace{
					{URL: "https://shard.example.test/services/workspaces/org"},
				},
			},
		}).
		Build()
	helper := presetTestKcpHelper{clients: map[string]client.Client{"root": workspaceTypeClient}}

	tests := []struct {
		name   string
		pc     corev1alpha1.ProviderConnection
		target rbacpresets.ServerTarget
		want   string
	}{
		{
			name: "workspace cluster",
			pc: corev1alpha1.ProviderConnection{
				Path: "root:platform-mesh-system",
			},
			target: rbacpresets.ServerTarget{Type: rbacpresets.ServerTargetWorkspaceCluster},
			want:   "https://frontproxy-front-proxy.platform-mesh-system:8443/clusters/root:platform-mesh-system",
		},
		{
			name:   "raw path",
			pc:     corev1alpha1.ProviderConnection{},
			target: rbacpresets.ServerTarget{Type: rbacpresets.ServerTargetRawPath, RawPath: "/services/marketplace"},
			want:   "https://frontproxy-front-proxy.platform-mesh-system:8443/services/marketplace",
		},
		{
			name: "path raw path",
			pc: corev1alpha1.ProviderConnection{
				Path:    "root:orgs",
				RawPath: ptr.To("/services/contentconfigurations"),
			},
			target: rbacpresets.ServerTarget{Type: rbacpresets.ServerTargetPathRawPath},
			want:   "https://frontproxy-front-proxy.platform-mesh-system:8443/services/contentconfigurations",
		},
		{
			name: "workspace type virtual workspace",
			pc: corev1alpha1.ProviderConnection{
				Path: "root",
			},
			target: rbacpresets.ServerTarget{
				Type:              rbacpresets.ServerTargetWorkspaceTypeVirtualWorkspace,
				WorkspaceTypeName: "org",
				WorkspaceTypePath: "root",
			},
			want: "https://frontproxy-front-proxy.platform-mesh-system:8443/services/workspaces/org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := buildPresetServerURL(context.Background(), helper, cfg, operatorCfg, instance, tt.pc, tt.target)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestApplyPresetManifestsCreatesObjectsInAnnotatedWorkspaces(t *testing.T) {
	t.Parallel()

	scheme := presetTestScheme(t)
	pmSystemClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	rootClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	rendered, err := rbacpresets.RenderPreset("test", []byte(`apiVersion: rbacpresets.platform-mesh.io/v1alpha1
kind: ProviderRBACPreset
metadata:
  name: test
spec:
  serverTarget:
    type: workspaceCluster
  serviceAccountWorkspace: root:platform-mesh-system
  serviceAccountName: platform-mesh-provider-test
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: platform-mesh-provider-test
  namespace: default
  annotations:
    rbacpresets.platform-mesh.io/workspace: root:platform-mesh-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: platform-mesh-provider-test-extra
  annotations:
    rbacpresets.platform-mesh.io/workspace: root
subjects:
  - kind: ServiceAccount
    name: platform-mesh-provider-test
    namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:kcp:workspace:access
`), rbacpresets.PresetTemplateData{})
	require.NoError(t, err)

	helper := presetTestKcpHelper{clients: map[string]client.Client{
		"root:platform-mesh-system": pmSystemClient,
		"root":                      rootClient,
	}}
	err = applyPresetManifests(context.Background(), helper, &rest.Config{}, rendered.ByWorkspace)
	require.NoError(t, err)

	var sa corev1.ServiceAccount
	require.NoError(t, pmSystemClient.Get(context.Background(), client.ObjectKey{Name: "platform-mesh-provider-test", Namespace: "default"}, &sa))
	var crb rbacv1.ClusterRoleBinding
	require.NoError(t, rootClient.Get(context.Background(), client.ObjectKey{Name: "platform-mesh-provider-test-extra"}, &crb))
	require.Empty(t, crb.GetAnnotations())
}

func TestWriteProviderPresetKubeconfigToSecret(t *testing.T) {
	t.Parallel()

	scheme := presetTestScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	kcpClient := tokenClient{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		token:  "preset-token",
	}
	ctx := presetTestContext()
	instance := &corev1alpha1.PlatformMesh{}
	pc := corev1alpha1.ProviderConnection{
		Path:               "root:platform-mesh-system",
		Secret:             "sample-kubeconfig",
		ProviderRBACPreset: ptr.To("sample"),
	}
	cfg := &rest.Config{Host: "https://root.kcp.test", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}
	helper := presetTestKcpHelper{clients: map[string]client.Client{
		"root:platform-mesh-system": kcpClient,
	}}

	err := writeProviderPresetKubeconfigToSecret(ctx, rbacpresets.NewLoader(rbacpresets.EmbeddedProvidersFS()), k8sClient, helper, cfg, instance, pc)
	require.NoError(t, err)

	var secret corev1.Secret
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKey{Name: "sample-kubeconfig", Namespace: "platform-mesh-system"}, &secret))
	kubeconfig, err := clientcmd.Load(secret.Data["kubeconfig"])
	require.NoError(t, err)
	cluster := kubeconfig.Clusters[kubeconfig.Contexts[kubeconfig.CurrentContext].Cluster]
	require.Equal(t, "https://frontproxy-front-proxy.platform-mesh-system:8443/clusters/root:platform-mesh-system", cluster.Server)
	authInfo := kubeconfig.AuthInfos[kubeconfig.Contexts[kubeconfig.CurrentContext].AuthInfo]
	require.Equal(t, "preset-token", authInfo.Token)
}

func TestHandleProviderConnectionRejectsPresetWithAPIExportSource(t *testing.T) {
	t.Parallel()

	subroutine := &ProvidersecretSubroutine{}
	ctx := presetTestContext()
	_, err := subroutine.HandleProviderConnection(ctx, &corev1alpha1.PlatformMesh{}, corev1alpha1.ProviderConnection{
		Secret:             "bad",
		ProviderRBACPreset: ptr.To("init-agent"),
		APIExportName:      ptr.To("core.platform-mesh.io"),
	}, &rest.Config{})
	require.ErrorContains(t, err, "providerRBACPreset is mutually exclusive")
}

func presetTestContext() context.Context {
	logCfg := logger.DefaultConfig()
	logCfg.Name = "preset-test"
	log, _ := logger.New(logCfg)
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, presetTestOperatorConfig())
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, log)
	return ctx
}

func presetTestOperatorConfig() config.OperatorConfig {
	return config.OperatorConfig{
		KCP: config.KCPConfig{
			Namespace:      "platform-mesh-system",
			FrontProxyName: "frontproxy",
			FrontProxyPort: "8443",
			RootShardName:  "rootshard",
		},
	}
}

func presetTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, authv1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	require.NoError(t, kcptenancyv1alpha.AddToScheme(scheme))
	return scheme
}

func TestCreateOrUpdatePresetManifestUpdatesExistingObjects(t *testing.T) {
	t.Parallel()

	scheme := presetTestScheme(t)
	kcpClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "preset-role"},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}},
	}).Build()
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata": map[string]interface{}{
			"name": "preset-role",
		},
		"rules": []interface{}{
			map[string]interface{}{
				"apiGroups": []interface{}{""},
				"resources": []interface{}{"pods"},
				"verbs":     []interface{}{"list"},
			},
		},
	}}

	err := createOrUpdatePresetManifest(context.Background(), kcpClient, obj)
	require.NoError(t, err)

	var role rbacv1.ClusterRole
	require.NoError(t, kcpClient.Get(context.Background(), client.ObjectKey{Name: "preset-role"}, &role))
	require.Equal(t, []string{"list"}, role.Rules[0].Verbs)
}

func TestCreateOrUpdatePresetManifestIgnoresAlreadyExistingNamespace(t *testing.T) {
	t.Parallel()

	scheme := presetTestScheme(t)
	kcpClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: defaultScopedSANamespace},
	}).Build()
	err := ensureScopedNamespaceExists(context.Background(), kcpClient, defaultScopedSANamespace)
	require.NoError(t, err)
}
