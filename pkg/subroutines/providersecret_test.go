package subroutines_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

var secretKubeconfigData, _ = os.ReadFile("test/kubeconfig.yaml")

type fakeHelm struct{ ready bool }

func (f fakeHelm) GetRelease(ctx context.Context, cli client.Client, name, ns string) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{"ready": f.ready},
	}}
	return u, nil
}

type ProvidersecretTestSuite struct {
	suite.Suite
	testObj *subroutines.ProvidersecretSubroutine
	// mocks
	clientMock *mocks.Client
	scheme     *runtime.Scheme
	log        *logger.Logger
}

func TestProvidersecretTestSuite(t *testing.T) {
	suite.Run(t, new(ProvidersecretTestSuite))
}

func (suite *ProvidersecretTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "ProvidersecretTestSuite"
	suite.log, _ = logger.New(cfg)

	suite.clientMock = new(mocks.Client)

	suite.scheme = runtime.NewScheme()
	_ = corev1.AddToScheme(suite.scheme)
	_ = corev1alpha1.AddToScheme(suite.scheme)
	_ = kcpapiv1alpha.AddToScheme(suite.scheme)
	_ = kcptenancyv1alpha.AddToScheme(suite.scheme)

	suite.clientMock.EXPECT().Scheme().Return(suite.scheme).Maybe()

	suite.testObj = subroutines.NewProviderSecretSubroutine(suite.clientMock, &subroutines.Helper{}, fakeHelm{ready: true}, "")
}

func (suite *ProvidersecretTestSuite) TearDownTest() {
	// clear mocks
	suite.clientMock = nil

	// clear test object
	suite.testObj = nil
}

func (s *ProvidersecretTestSuite) TestProcess() {
	instance := &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "provider-secret",
					},
				},
			},
		},
		Status: corev1alpha1.PlatformMeshStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{Name: "root:platform-mesh-system", Phase: "Ready"},
				{Name: "root:orgs", Phase: "Ready"},
			},
		},
	}

	kubeconfig, err := clientcmd.Load(secretKubeconfigData)
	s.Require().NoError(err)

	kubeconfig.Contexts["custom-context"] = &clientcmdapi.Context{
		AuthInfo: "test-user",
		Cluster:  "custom-cluster",
	}

	if _, exists := kubeconfig.Clusters["custom-cluster"]; !exists {
		kubeconfig.Clusters["custom-cluster"] = &clientcmdapi.Cluster{}
	}
	kubeconfig.Clusters["custom-cluster"].Server = "http://dummy-url" // value replaced below
	kubeconfig.Contexts["custom-context"] = &clientcmdapi.Context{
		AuthInfo: "test-user",
		Cluster:  "custom-cluster",
	}
	kubeconfig.CurrentContext = "custom-context"

	patchedData, err := clientcmd.Write(*kubeconfig)
	s.Require().NoError(err)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": patchedData,
		},
	}

	s.clientMock.EXPECT().Get(mock.Anything, mock.MatchedBy(func(key types.NamespacedName) bool {
		return key.Name == "test-secret" && key.Namespace == "default"
	}), mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = secret
			return nil
		}).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.MatchedBy(func(key types.NamespacedName) bool {
		return key.Name == "provider-secret" && key.Namespace == "default"
	}), mock.Anything).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "Secret"}, "provider-secret")).
		Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	s.clientMock.EXPECT().Create(
		mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				s.log.Error().Msg("Object is not a Secret")
				return false
			}

			if secret.Name != "provider-secret" {
				s.log.Error().Msgf("Secret name mismatch: expected 'provider-secret', got '%s'", secret.Name)
				return false
			}
			if secret.Namespace != "default" {
				s.log.Error().Msgf("Secret namespace mismatch: expected 'default', got '%s'", secret.Namespace)
				return false
			}

			kubeconfigData, exists := secret.Data["kubeconfig"]
			if !exists {
				s.log.Error().Msg("kubeconfig data not found in secret")
				return false
			}

			kubeconfig, err := clientcmd.Load(kubeconfigData)
			if err != nil {
				s.log.Error().Msgf("Failed to parse kubeconfig: %v", err)
				return false
			}

			currentContext := kubeconfig.Contexts[kubeconfig.CurrentContext]
			cluster := kubeconfig.Clusters[currentContext.Cluster]

			// Test that the URL is passed correctly form the endpoint slice
			expectedURL := "http://example.com/clusters/root:platform-mesh-system"
			if cluster.Server != expectedURL {
				s.log.Error().Msgf("Server URL mismatch: expected '%s', got '%s'", expectedURL, cluster.Server)
				return false
			}
			return true
		}),
		mock.Anything,
	).
		RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			providerSecret := obj.(*corev1.Secret)
			err := controllerutil.SetOwnerReference(instance, providerSecret, s.clientMock.Scheme())
			s.NoError(err)
			return nil
		}).
		Once()

	scheme := runtime.NewScheme()
	err = corev1alpha1.AddToScheme(scheme)
	s.Require().NoError(err)
	s.clientMock.EXPECT().Scheme().Return(scheme).Once()

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "http://example.com"},
			},
		},
	}

	mockKcpClient := new(mocks.Client)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"kubeconfig": patchedData,
				},
			}
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)
	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestWrongScheme() {
	instance := &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "test-secret",
					},
				},
			},
		},
		Status: corev1alpha1.PlatformMeshStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{
					Name:  "root:platform-mesh-system",
					Phase: "Ready",
				},
				{
					Name:  "root:orgs",
					Phase: "Ready",
				},
			},
		},
	}

	// mocks
	mockK8sClient := new(mocks.Client)
	mockK8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockK8sClient.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	// return nil scheme
	mockK8sClient.EXPECT().Scheme().Return(nil).Maybe()

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mockK8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			switch obj := o.(type) {
			case *kcpapiv1alpha.APIExportEndpointSlice:
				*obj = *slice
				return nil
			case *unstructured.Unstructured:
				// do nothing
				return nil
			default:
				return fmt.Errorf("unexpected type %T", o)
			}
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockK8sClient, nil).Once()
	mockK8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	mockK8sClient.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"kubeconfig": secretKubeconfigData,
				},
			}
			return nil
		},
	).Once()

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProviderSecretSubroutine(mockK8sClient, mockedKcpHelper, fakeHelm{ready: true}, "")

	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)
	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Error(opErr.Err(), "expected error due to nil scheme")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestErrorCreatingSecret() {
	instance := &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "test-secret",
					},
				},
			},
		},
		Status: corev1alpha1.PlatformMeshStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{Name: "root:platform-mesh-system", Phase: "Ready"},
			},
		},
	}

	// Setup test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "http://url"},
			},
		},
	}

	// Mocks
	mockClient := new(mocks.Client)
	mockScheme := runtime.NewScheme()

	// Expect scheme call for SetOwnerReference
	mockClient.EXPECT().
		Scheme().
		Return(mockScheme).
		Maybe()

	// Simulate that secret doesn't exist, so Create is triggered
	mockClient.EXPECT().
		Get(mock.Anything, mock.MatchedBy(func(key client.ObjectKey) bool {
			return key.Name == "test-secret"
		}), mock.Anything).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "Secret"}, "test-secret")).
		Once()

	mockClient.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	mockClient.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"cluster-admin-secret":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		})

	// Simulate error on Create
	mockClient.EXPECT().
		Create(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("error creating secret")).
		Once()

	// Mock KCP client and its Get call for EndpointSlice
	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
			_, ok := obj.(*kcpapiv1alpha.APIExportEndpointSlice)
			return ok
		})).
		RunAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).
		Once()

	// Mock KcpHelper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"kubeconfig": secretKubeconfigData,
				},
			}
			return nil
		},
	).Once()

	// Run
	s.testObj = subroutines.NewProviderSecretSubroutine(mockClient, mockedKcpHelper, fakeHelm{ready: true}, "example.com")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	operatorCfg.KCP.ClusterAdminSecretName = "cluster-admin-secret"
	operatorCfg.KCP.Namespace = "platform-mesh-system"

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg) // Add this line
	res, opErr := s.testObj.Process(ctx, instance)

	// Asserts
	s.Require().NotNil(opErr, "expected opErr to not be nil")
	s.Assert().Error(opErr.Err(), "expected error from operator")
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestFailedBuilidingKubeconfig() {
	instance := &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "test-secret",
					},
				},
			},
		},
		Status: corev1alpha1.PlatformMeshStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{
					Name:  "root:platform-mesh-system",
					Phase: "Ready",
				},
				{
					Name:  "root:orgs",
					Phase: "Ready",
				},
			},
		},
	}

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	// mocks
	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mockKcpClient := new(mocks.Client)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"kubeconfig": []byte("invalid"),
				},
			}
			return nil
		},
	).Once()

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg) // Add this line
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "Failed to build config from kubeconfig string")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestErrorGettingSecret() {
	instance := &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "test-secret",
					},
				},
			},
		},
		Status: corev1alpha1.PlatformMeshStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{
					Name:  "root:platform-mesh-system",
					Phase: "Ready",
				},
				{
					Name:  "root:orgs",
					Phase: "Ready",
				},
			},
		},
	}

	// mock client.Get
	secret := corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": []byte("invalid"),
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	s.clientMock.EXPECT().Get(
		mock.Anything, mock.Anything, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = secret
			return errors.New("error getting secret")
		}).Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)

	// assert
	s.Assert().Error(opErr.Err(), "Failed to build kubeconfig")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers(s.getBaseInstance())
	s.Assert().Equal(res, []string{subroutines.ProvidersecretSubroutineFinalizer})
}

func (s *ProvidersecretTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.ProvidersecretSubroutineName)
}

func (suite *ProvidersecretTestSuite) TestConstructor() {
	helper := &subroutines.Helper{}
	suite.testObj = subroutines.NewProviderSecretSubroutine(suite.clientMock, helper, fakeHelm{ready: true}, "")
}

func (s *ProvidersecretTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), nil)
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) getBaseInstance() *corev1alpha1.PlatformMesh {
	return &corev1alpha1.PlatformMesh{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PlatformMesh",
			APIVersion: "core.platform-mesh.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Kcp: corev1alpha1.Kcp{
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: ptr.To("test-endpoint"),
						Path:              "root:platform-mesh-system",
						Secret:            "provider-secret",
					},
				},
			},
		},
	}
}

func (s *ProvidersecretTestSuite) TestInvalidKubeconfig() {
	instance := s.getBaseInstance()
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": []byte("invalid kubeconfig data"),
		},
	}

	s.clientMock.EXPECT().Get(mock.Anything, mock.MatchedBy(func(key types.NamespacedName) bool {
		return key.Name == "test-secret" && key.Namespace == "default"
	}), mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = secret
			return nil
		}).Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	mockedKcpHelper := new(mocks.KcpHelper)
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestErrorLoadingKubeconfig() {
	instance := s.getBaseInstance()
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": []byte("invalid kubeconfig data"),
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		})
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *badSecret
			return nil
		})

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestErrorCreatingKCPClient() {
	instance := s.getBaseInstance()
	badKubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		})

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *badKubeconfigSecret
			return nil
		}).Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to create KCP client")).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *badKubeconfigSecret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)

	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestErrorGettingAPIExportEndpointSlice() {
	instance := s.getBaseInstance()
	// mock getting rootShard and frontproxy
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("failed to get APIExportEndpointSlice")).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)

	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestEmptyAPIExportEndpoints() {
	instance := s.getBaseInstance()
	// mock getting rootShard and frontproxy
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{},
		},
	}

	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestInvalidEndpointURL() {
	instance := s.getBaseInstance()
	// mock getting rootShard and frontproxy
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "://invalid-url"},
			},
		},
	}

	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestContextNotFoundInKubeconfig() {
	instance := s.getBaseInstance()
	kubeconfig := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"test-cluster": {
				Server: "https://test-server",
			},
		},
		Contexts:       map[string]*clientcmdapi.Context{},
		CurrentContext: "non-existent-context",
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"test-user": {},
		},
	}

	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	s.Require().NoError(err)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigBytes,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "http://example.com"},
			},
		},
	}

	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestClusterNotFoundInKubeconfig() {
	instance := s.getBaseInstance()
	kubeconfig := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{},
		Contexts: map[string]*clientcmdapi.Context{
			"test-context": {
				Cluster:  "non-existent-cluster",
				AuthInfo: "test-user",
			},
		},
		CurrentContext: "test-context",
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"test-user": {},
		},
	}

	kubeconfigBytes, err := clientcmd.Write(*kubeconfig)
	s.Require().NoError(err)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfigBytes,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	// Mock the Helm release lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp", Namespace: "default"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			release := obj.(*unstructured.Unstructured)
			release.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Ready",
							"status": "True",
						},
					},
				},
			}
			return nil
		})

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "http://example.com"},
			},
		},
	}

	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestHandleProviderConnections() {
	// Save and restore default connections
	oldInits := subroutines.DefaultInitializerConnection
	defer func() { subroutines.DefaultInitializerConnection = oldInits }()
	subroutines.DefaultInitializerConnection = nil

	// Setup test instance
	instance := s.getBaseInstance()
	instance.Spec.Kcp.ProviderConnections = nil
	instance.Spec.Kcp.ExtraProviderConnections = []corev1alpha1.ProviderConnection{
		{
			EndpointSliceName: ptr.To(""),
			Path:              "root:platform-mesh-system",
			Secret:            "external-kubeconfig",
			External:          true,
			Namespace:         ptr.To("test"),
		},
	}
	instance.Spec.Exposure = &corev1alpha1.ExposureConfig{
		BaseDomain: "example.com",
		Port:       8443,
		Protocol:   "https",
	}

	// Setup test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	// Setup mock expectations for Get
	s.clientMock.
		EXPECT().
		Get(
			mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				return key.Name == "test-secret" && key.Namespace == "test"
			}),
			mock.Anything,
		).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*corev1.Secret) = *secret
			return nil
		}).
		Once()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()

	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"iam-service-kubeconfig",
						"portal-kubeconfig":
						return true
					}
				}
				if key.Namespace == "test" {
					switch key.Name {
					case "external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})
	s.clientMock.EXPECT().
		Update(mock.Anything,
			mock.Anything).
		RunAndReturn(func(_ context.Context, obj client.Object, _ ...client.UpdateOption) error {
			sec := obj.(*corev1.Secret)
			if sec.Name == "external-kubeconfig" {
				if data, ok := sec.Data["kubeconfig"]; ok {
					cfg, err := clientcmd.Load(data)
					if err != nil {
						s.log.Error().Msgf("failed to parse kubeconfig: %v", err)
					} else {
						for _, c := range cfg.Clusters {
							if c != nil {
								if c.Server == "https://kcp.api.example.com:8443/clusters/root:platform-mesh-system" {
									return nil
								}
								return fmt.Errorf("unexpected server URL: %s", c.Server)
							}
							break
						}
					}
				}
			}
			return nil
		})

	// Setup mock KCP client
	mockedKcpClient := new(mocks.Client)
	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{URL: "http://example.com"},
			},
		},
	}
	mockedKcpClient.
		EXPECT().
		Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).
		Times(len(subroutines.DefaultProviderConnections))

	// Setup mock KCP helper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.
		EXPECT().
		NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).
		Times(len(subroutines.DefaultProviderConnections))
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	// Setup mock expectations for each provider connection
	for _, pc := range subroutines.DefaultProviderConnections {
		s.clientMock.
			EXPECT().
			Get(
				mock.Anything,
				types.NamespacedName{Name: pc.Secret, Namespace: instance.Namespace},
				mock.Anything,
			).
			Return(apierrors.NewNotFound(
				schema.GroupResource{Group: "", Resource: "secrets"},
				pc.Secret,
			)).
			Once()

		s.clientMock.
			EXPECT().
			Create(
				mock.Anything,
				mock.MatchedBy(func(obj client.Object) bool {
					sec, ok := obj.(*corev1.Secret)
					if !ok {
						s.log.Error().Msgf("expected a *corev1.Secret, got %T", obj)
						return false
					}
					if sec.Name != pc.Secret || sec.Namespace != instance.Namespace {
						s.log.Error().Msgf("Secret %s/%s; want %s/%s",
							sec.Namespace, sec.Name,
							instance.Namespace, pc.Secret)
						return false
					}
					data, ok := sec.Data["kubeconfig"]
					if !ok {
						s.log.Error().Msg("missing kubeconfig key")
						return false
					}
					cfg, err := clientcmd.Load(data)
					if err != nil {
						s.log.Error().Msgf("invalid kubeconfig: %v", err)
						return false
					}
					ctx := cfg.Contexts[cfg.CurrentContext]
					cluster := cfg.Clusters[ctx.Cluster]
					want := fmt.Sprintf("http://example.com/clusters/%s", pc.Path)
					if cluster.Server != want {
						s.log.Error().Msgf("server URL = %q; want %q", cluster.Server, want)
						return false
					}
					return true
				}),
				mock.Anything,
			).
			RunAndReturn(func(ctx context.Context, obj client.Object, _ ...client.CreateOption) error {
				providerSecret := obj.(*corev1.Secret)
				err := controllerutil.SetOwnerReference(instance, providerSecret, s.clientMock.Scheme())
				s.NoError(err)
				return nil
			}).
			Once()
	}

	// Run test
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "example.com")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg) // Add this line
	res, opErr := s.testObj.Process(ctx, instance)
	s.Require().Nil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
}

func (s *ProvidersecretTestSuite) TestHandleInitializerConnection() {
	// Save and restore default connections
	oldProv := subroutines.DefaultProviderConnections
	defer func() { subroutines.DefaultProviderConnections = oldProv }()
	subroutines.DefaultProviderConnections = nil

	// Setup test instance
	instance := s.getBaseInstance()
	instance.Spec.Kcp.ProviderConnections = nil
	instance.Spec.Kcp.InitializerConnections = []corev1alpha1.InitializerConnection{
		{
			WorkspaceTypeName: "test-workspace",
			Path:              "root:test-path",
			Secret:            "test-initializer-secret",
		},
	}

	// Setup test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}

	// Setup mock KCP client
	mockedKcpClient := new(mocks.Client)
	// Get the expected path from the instance
	path := instance.Spec.Kcp.InitializerConnections[0].Path
	fullURL := fmt.Sprintf("http://example.com/clusters/%s", path)
	workspaceType := &kcptenancyv1alpha.WorkspaceType{
		Status: kcptenancyv1alpha.WorkspaceTypeStatus{
			VirtualWorkspaces: []kcptenancyv1alpha.VirtualWorkspace{
				{URL: fullURL},
			},
		},
	}
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcptenancyv1alpha.WorkspaceType) = *workspaceType
			return nil
		}).Once()

	// Setup mock KCP helper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	// Setup mock expectations for Get and Create
	s.clientMock.EXPECT().Get(
		mock.Anything,
		types.NamespacedName{Name: "test-initializer-secret", Namespace: "platform-mesh-system"},
		mock.Anything,
	).Return(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "secrets"}, "test-initializer-secret")).Once()

	// We expect the URL to be rewritten to use the front-proxy, not example.com.
	// Declare expectedURL here so the matcher can capture it and we set it after operatorCfg is prepared.
	var expectedURL string

	var createdSecret *corev1.Secret
	s.clientMock.EXPECT().Create(
		mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			sec := obj.(*corev1.Secret)
			createdSecret = sec.DeepCopy()
			cfg, err := clientcmd.Load(sec.Data["kubeconfig"])
			if err != nil {
				return false
			}
			ctx := cfg.Contexts[cfg.CurrentContext]
			cluster := cfg.Clusters[ctx.Cluster]
			// Verify it matches the rewritten virtual workspace URL (front-proxy)
			return cluster.Server == expectedURL
		}),
		// mock.Anything,
	).
		RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			return nil
		}).
		Once()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	s.Require().NoError(err)

	// Run test
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	// Use base name; code appends "-front-proxy"
	operatorCfg.KCP.FrontProxyName = "test"
	operatorCfg.KCP.FrontProxyPort = "8080"

	// Expected rewritten URL: http://<front-proxy-name>-front-proxy:<port>/clusters/<path>
	expectedURL = fmt.Sprintf(
		"http://%s-front-proxy:%s/clusters/%s",
		operatorCfg.KCP.FrontProxyName,
		operatorCfg.KCP.FrontProxyPort,
		path,
	)

	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)
	res, opErr := s.testObj.HandleInitializerConnection(ctx, instance, instance.Spec.Kcp.InitializerConnections[0], restCfg)

	// Assert
	s.Require().Nil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
	s.Require().NotNil(createdSecret)
	s.Require().NotNil(createdSecret.Data)
	s.Require().Contains(createdSecret.Data, "kubeconfig")

	// Verify kubeconfig is valid and points to virtual workspace
	kubeconfig, err := clientcmd.Load(createdSecret.Data["kubeconfig"])
	s.Require().NoError(err)
	s.Require().NotNil(kubeconfig)

	// Verify current context exists and points to correct cluster
	s.Require().NotEmpty(kubeconfig.CurrentContext)
	context := kubeconfig.Contexts[kubeconfig.CurrentContext]
	s.Require().NotNil(context)

	// Verify cluster exists and points to virtual workspace
	cluster := kubeconfig.Clusters[context.Cluster]
	s.Require().NotNil(cluster)
	s.Require().Equal(expectedURL, cluster.Server)
}

func (s *ProvidersecretTestSuite) TestInitializerConnectionErrorGettingWorkspaceType() {
	// Setup test instance and initializer connection
	instance := s.getBaseInstance()
	ic := corev1alpha1.InitializerConnection{
		WorkspaceTypeName: "test-workspace",
		Path:              "root:test-path",
		Secret:            "test-initializer-secret",
	}

	// Setup test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}

	// Setup mock KCP client
	mockedKcpClient := new(mocks.Client)
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("failed to get workspace type")).Once()

	// Setup mock KCP helper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	s.Require().NoError(err)

	// Run test
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.HandleInitializerConnection(ctx, instance, ic, restCfg)

	// Assert
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
	s.Assert().Contains(opErr.Err().Error(), "failed to get workspace type")
}

func (s *ProvidersecretTestSuite) TestInitializerConnectionNoVirtualWorkspaces() {
	// Save and restore default connections
	oldProv := subroutines.DefaultProviderConnections
	defer func() { subroutines.DefaultProviderConnections = oldProv }()
	subroutines.DefaultProviderConnections = nil

	// Setup test instance
	instance := s.getBaseInstance()
	instance.Spec.Kcp.ProviderConnections = nil
	instance.Spec.Kcp.InitializerConnections = []corev1alpha1.InitializerConnection{
		{
			WorkspaceTypeName: "test-workspace",
			Path:              "root:test-path",
			Secret:            "test-initializer-secret",
		},
	}

	// Setup test secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
			"ca.crt":     []byte("ZHVtbXlkYXRhCg=="),
			"tls.crt":    []byte("ZHVtbXlkYXRhCg=="),
			"tls.key":    []byte("ZHVtbXlkYXRhCg=="),
		},
	}

	// Setup scheme
	scheme := runtime.NewScheme()
	s.Require().NoError(corev1alpha1.AddToScheme(scheme))
	s.Require().NoError(corev1.AddToScheme(scheme))
	s.Require().NoError(kcptenancyv1alpha.AddToScheme(scheme))
	s.clientMock.EXPECT().Scheme().Return(scheme).Maybe()

	// Setup mock expectations for Get
	s.clientMock.EXPECT().Get(
		mock.Anything,
		mock.Anything,
		// mock.MatchedBy(func(key types.NamespacedName) bool {
		// 	return key.Name == "test-secret" && key.Namespace == "default"
		// }),
		mock.AnythingOfType("*v1.Secret"),
	).RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
		*obj.(*corev1.Secret) = *secret
		return nil
	}).Once()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.Anything,
			mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			rootShard := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Available",
								"status": "True",
							},
						},
					},
				},
			}
			*obj.(*unstructured.Unstructured) = *rootShard
			return nil
		}).
		Twice()
	s.clientMock.EXPECT().
		Get(mock.Anything,
			mock.MatchedBy(func(key types.NamespacedName) bool {
				if key.Namespace == "platform-mesh-system" {
					switch key.Name {
					case "account-operator-kubeconfig",
						"rebac-authz-webhook-kubeconfig",
						"security-operator-kubeconfig",
						"kubernetes-grapqhl-gateway-kubeconfig",
						"extension-manager-operator-kubeconfig",
						"portal-kubeconfig",
						"external-kubeconfig":
						return true
					}
				}
				return false
			}),
			mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// *obj.(*corev1.Secret) = *secret
			return nil
		})

	// Setup mock KCP client
	mockedKcpClient := new(mocks.Client)
	workspaceType := &kcptenancyv1alpha.WorkspaceType{
		Status: kcptenancyv1alpha.WorkspaceTypeStatus{
			VirtualWorkspaces: []kcptenancyv1alpha.VirtualWorkspace{},
		},
	}
	mockedKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcptenancyv1alpha.WorkspaceType) = *workspaceType
			return nil
		}).Once()

	// Setup mock KCP helper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = *secret
			return nil
		},
	).Once()

	// Run test
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "example.com")

	// Add the missing operator config context
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.Background(), keys.ConfigCtxKey, operatorCfg) // Add this line
	ctx = context.WithValue(ctx, keys.LoggerCtxKey, s.log)

	res, opErr := s.testObj.Process(ctx, instance)

	// Assert
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
	s.Assert().Contains(opErr.Err().Error(), "no virtual workspaces found")
}

func (s *ProvidersecretTestSuite) TestInitializerConnectionErrorCreatingSecret() {
	// Setup test instance and initializer connection
	instance := s.getBaseInstance()
	ic := corev1alpha1.InitializerConnection{
		WorkspaceTypeName: "test-workspace",
		Path:              "root:test-path",
		Secret:            "test-initializer-secret",
		// Setup test secret
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}

	// Setup mock KCP client
	mockedKcpClient := new(mocks.Client)
	// Get the expected path from the instance
	path := ic.Path
	fullURL := fmt.Sprintf("http://example.com/clusters/%s", path)
	workspaceType := &kcptenancyv1alpha.WorkspaceType{
		Status: kcptenancyv1alpha.WorkspaceTypeStatus{
			VirtualWorkspaces: []kcptenancyv1alpha.VirtualWorkspace{
				{URL: fullURL},
			},
		},
	}
	// Narrow matcher to only WorkspaceType gets to avoid catching Secret gets
	mockedKcpClient.EXPECT().Get(
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			_, ok := obj.(*kcptenancyv1alpha.WorkspaceType)
			return ok
		}),
	).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			*obj.(*kcptenancyv1alpha.WorkspaceType) = *workspaceType
			return nil
		}).Once()

	// Setup mock KCP helper
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mockedKcpClient, nil).Once()

	// Setup mock expectations for Get and Create on the main k8s client
	// Pre-check Get, and CreateOrUpdate will call Get again internally: allow 2 calls
	s.clientMock.EXPECT().Get(
		mock.Anything,
		types.NamespacedName{Name: "test-initializer-secret", Namespace: "platform-mesh-system"},
		mock.Anything,
	).Return(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "secrets"}, "test-initializer-secret")).Twice()

	// Helm/unstructured lookup
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).Return(nil)

	// Expected rewritten URL via front-proxy
	var expectedURL string
	s.clientMock.EXPECT().Create(
		mock.Anything,
		mock.MatchedBy(func(obj client.Object) bool {
			sec := obj.(*corev1.Secret)
			kubeconfigData := sec.Data["kubeconfig"]
			cfg, err := clientcmd.Load(kubeconfigData)
			if err != nil {
				return false
			}
			ctx := cfg.Contexts[cfg.CurrentContext]
			cluster := cfg.Clusters[ctx.Cluster]
			// Verify it matches the rewritten virtual workspace URL (front-proxy)
			return cluster.Server == expectedURL
		}),
		mock.Anything,
	).
		Return(errors.New("failed to create secret")).Once()

	restCfg, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["kubeconfig"])
	s.Require().NoError(err)

	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	// Ensure URL rewriter has values
	operatorCfg.KCP.FrontProxyName = "test"
	operatorCfg.KCP.FrontProxyPort = "8080"

	// Compute expected rewritten URL
	expectedURL = fmt.Sprintf(
		"http://%s-front-proxy:%s/clusters/%s",
		operatorCfg.KCP.FrontProxyName,
		operatorCfg.KCP.FrontProxyPort,
		path,
	)

	// Run
	// Use the main client mock (not the KCP client) for the subroutine
	s.testObj = subroutines.NewProviderSecretSubroutine(s.clientMock, mockedKcpHelper, fakeHelm{ready: true}, "")
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)
	res, opErr := s.testObj.HandleInitializerConnection(ctx, instance, ic, restCfg)

	// Assert
	s.Require().NotNil(opErr)
	s.Assert().Equal(ctrl.Result{}, res)
	s.Assert().Contains(opErr.Err().Error(), "failed to create secret")
}
