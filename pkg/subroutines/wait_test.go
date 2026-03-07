package subroutines_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WaitTestSuite struct {
	suite.Suite
	clientMock        *mocks.Client // infra cluster — resource readiness checks
	runtimeClientMock *mocks.Client // runtime cluster — KCP secret access
	kcpClientMock     *mocks.Client
	kcpHelperMock     *mocks.KcpHelper
	testObj           *subroutines.WaitSubroutine
	log               *logger.Logger
	cfg               config.OperatorConfig
}

func TestWaitTestSuite(t *testing.T) {
	suite.Run(t, new(WaitTestSuite))
}

func (s *WaitTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.runtimeClientMock = new(mocks.Client)
	s.kcpClientMock = new(mocks.Client)
	s.kcpHelperMock = new(mocks.KcpHelper)
	s.cfg = config.OperatorConfig{}
	s.cfg.KCP.ClusterAdminSecretName = "kcp-admin-secret"
	s.cfg.KCP.Namespace = "platform-mesh-system"

	logCfg := logger.DefaultConfig()
	logCfg.Level = "debug"
	logCfg.NoJSON = true
	logCfg.Name = "WaitTestSuite"
	s.log, _ = logger.New(logCfg)
	s.testObj = subroutines.NewWaitSubroutine(s.clientMock, s.runtimeClientMock, &s.cfg, s.kcpHelperMock)
}

func (s *WaitTestSuite) TearDownTest() {
	s.clientMock = nil
	s.runtimeClientMock = nil
	s.kcpClientMock = nil
	s.kcpHelperMock = nil
	s.testObj = nil
}

func (s *WaitTestSuite) mockWorkspaceAuthConfigCheck(audience string) {
	s.runtimeClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-admin-secret", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt":  []byte("fake-ca"),
				"tls.crt": []byte("fake-cert"),
				"tls.key": []byte("fake-key"),
			}
			return nil
		})

	s.kcpHelperMock.EXPECT().
		NewKcpClient(mock.Anything, "root").
		Return(s.kcpClientMock, nil)

	s.kcpClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs-authentication"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			wac := obj.(*unstructured.Unstructured)
			wac.Object = map[string]any{
				"spec": map[string]any{
					"jwt": []any{
						map[string]any{
							"issuer": map[string]any{
								"audiences": []any{audience},
							},
						},
					},
				},
			}
			return nil
		})
}

func (s *WaitTestSuite) TestProcess_NoResourcesExist() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: nil,
		},
	}

	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return nil
		}).Once() // Called once for the default resource type

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_AllResourcesReady() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: nil,
		},
	}

	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			unstructuredList.Items = []unstructured.Unstructured{{
				Object: map[string]any{
					"apiVersion": "helm.toolkit.fluxcd.io/v2",
					"kind":       "HelmRelease",
					"metadata": map[string]any{
						"name":      "test-helmrelease",
						"namespace": "platform-mesh-system",
						"labels": map[string]interface{}{
							"core.platform-mesh.io/operator-created": "true",
						},
					},
					"status": map[string]any{
						"conditions": []any{
							map[string]any{"type": "Ready", "status": "True"},
						},
					},
				},
			}}
			return nil
		}).Once()

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_ResourceNotReady() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: nil,
		},
	}

	// Mock List call returning not ready resource
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			notReadyResource := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "helm.toolkit.fluxcd.io/v2",
					"kind":       "HelmRelease",
					"metadata": map[string]interface{}{
						"name":      "test-helmrelease",
						"namespace": "platform-mesh-system",
						"labels": map[string]interface{}{
							"core.platform-mesh.io/operator-created": "true",
						},
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Ready",
								"status": "False",
							},
						},
					},
				},
			}
			unstructuredList.Items = []unstructured.Unstructured{notReadyResource}
			return nil
		}).Once()

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "is not ready yet")
}

func (s *WaitTestSuite) TestProcess_ListError() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: nil,
		},
	}

	// Mock List call returning error
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		Return(errors.New("mock list error")).
		Once()

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "mock list error")
}

func (s *WaitTestSuite) TestProcess_MultipleResourceTypes() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: nil,
		},
	}

	// Mock List calls for default resource type with label selector
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)

			readyResource := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "helm.toolkit.fluxcd.io/v2",
					"kind":       "HelmRelease",
					"metadata": map[string]interface{}{
						"name":      "test-helmrelease",
						"namespace": "platform-mesh-system",
						"labels": map[string]interface{}{
							"core.platform-mesh.io/operator-created": "true",
						},
					},
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{
								"type":   "Ready",
								"status": "True",
							},
						},
					},
				},
			}
			unstructuredList.Items = []unstructured.Unstructured{readyResource}
			return nil
		}).Once()

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestGetName() {
	name := s.testObj.GetName()
	s.Assert().Equal(subroutines.WaitSubroutineName, name)
}

func (s *WaitTestSuite) TestProcess_CustomResourceType_Kustomization() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						Versions:  []string{"v1"},
						Group:     "kustomize.toolkit.fluxcd.io",
						Kind:      "Kustomization",
						Namespace: "flux-system",
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "platform-mesh",
							},
						},
						ConditionStatus: metav1.ConditionTrue,
						ConditionType:   "Ready",
					},
				},
			},
		},
	}

	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			unstructuredList.Items = []unstructured.Unstructured{{
				Object: map[string]any{
					"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
					"kind":       "Kustomization",
					"metadata":   map[string]any{"name": "test-kustomization", "namespace": "flux-system"},
					"status":     map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
				},
			}}
			return nil
		}).Once()

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

// emptyWaitInstance returns a PlatformMesh with an empty WaitConfig so the resource
// loop is skipped and only the audience check is exercised.
func (s *WaitTestSuite) emptyWaitInstance() *corev1alpha1.PlatformMesh {
	return &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "test-mesh", Namespace: "default"},
		Spec:       corev1alpha1.PlatformMeshSpec{Wait: &corev1alpha1.WaitConfig{}},
	}
}

// mockSecretGet sets up the admin secret Get call to succeed with dummy TLS data.
func (s *WaitTestSuite) mockSecretGet() {
	s.runtimeClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-admin-secret", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			obj.(*corev1.Secret).Data = map[string][]byte{
				"ca.crt":  []byte("fake-ca"),
				"tls.crt": []byte("fake-cert"),
				"tls.key": []byte("fake-key"),
			}
			return nil
		})
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_SecretNotFound() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.runtimeClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-admin-secret", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "kcp-admin-secret"))

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to build kubeconfig")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_BuildKubeconfigError() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.runtimeClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-admin-secret", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
		Return(errors.New("internal server error"))

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to build kubeconfig")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_NewKcpClientError() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockSecretGet()
	s.kcpHelperMock.EXPECT().
		NewKcpClient(mock.Anything, "root").
		Return(nil, errors.New("invalid kubeconfig"))

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to create KCP client")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_WACNotFound() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockSecretGet()
	s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root").Return(s.kcpClientMock, nil)
	s.kcpClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs-authentication"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "workspaceauthenticationconfigurations"}, "orgs-authentication"))

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to get WorkspaceAuthenticationConfiguration")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_WACConnectivityError() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockSecretGet()
	s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root").Return(s.kcpClientMock, nil)
	s.kcpClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs-authentication"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(&url.Error{Op: "Get", URL: "https://kcp", Err: errors.New("connection refused")})

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to get WorkspaceAuthenticationConfiguration")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_WACGetError() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockSecretGet()
	s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root").Return(s.kcpClientMock, nil)
	s.kcpClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs-authentication"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewInternalError(errors.New("etcd unavailable")))

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "failed to get WorkspaceAuthenticationConfiguration")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_WACMissingJWT() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockSecretGet()
	s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root").Return(s.kcpClientMock, nil)
	s.kcpClientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs-authentication"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			obj.(*unstructured.Unstructured).Object = map[string]any{"spec": map[string]any{}}
			return nil
		})

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "no spec.jwt entries")
}

func (s *WaitTestSuite) TestCheckWorkspaceAuthConfigAudience_WACPlaceholder() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	s.mockWorkspaceAuthConfigCheck("<placeholder>")

	result, err := s.testObj.Process(ctx, s.emptyWaitInstance())

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "<placeholder>")
}

func (s *WaitTestSuite) TestFinalize() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
	}

	result, err := s.testObj.Finalize(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_ResourceByName_Ready() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						Versions:        []string{"v2"},
						Group:           "helm.toolkit.fluxcd.io",
						Kind:            "HelmRelease",
						Namespace:       "default",
						Name:            "test-helmrelease",
						ConditionStatus: metav1.ConditionTrue,
						ConditionType:   "Ready",
					},
				},
			},
		},
	}

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "test-helmrelease"}, mock.Anything).
		// List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstructuredObj := obj.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]any{
				"apiVersion": "helm.toolkit.fluxcd.io/v2",
				"kind":       "HelmRelease",
				"metadata":   map[string]any{"name": "platform-mesh-operator-components", "namespace": "default"},
				"status":     map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			}
			return nil
		})

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_ArgoCD_Application_Synced() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						Versions:  []string{"v1alpha1"},
						Group:     "argoproj.io",
						Kind:      "Application",
						Namespace: "platform-mesh-system",
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"core.platform-mesh.io/operator-created": "true",
							},
						},
						// Use StatusFieldPath for ArgoCD Application sync status
						StatusFieldPath: []string{"status", "sync", "status"},
						StatusValue:     "Synced",
					},
				},
			},
		},
	}

	// Mock List call returning synced ArgoCD Application
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			syncedApp := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "infra",
						"namespace": "platform-mesh-system",
						"labels": map[string]interface{}{
							"core.platform-mesh.io/operator-created": "true",
						},
					},
					"status": map[string]interface{}{
						"sync": map[string]interface{}{
							"status": "Synced",
						},
						"health": map[string]interface{}{
							"status": "Healthy",
						},
					},
				},
			}
			unstructuredList.Items = []unstructured.Unstructured{syncedApp}
			return nil
		}).Once()

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_ArgoCD_Application_NotSynced() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						Versions:  []string{"v1alpha1"},
						Group:     "argoproj.io",
						Kind:      "Application",
						Namespace: "platform-mesh-system",
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"core.platform-mesh.io/operator-created": "true",
							},
						},
						// Use StatusFieldPath for ArgoCD Application sync status
						StatusFieldPath: []string{"status", "sync", "status"},
						StatusValue:     "Synced",
					},
				},
			},
		},
	}

	// Mock List call returning OutOfSync ArgoCD Application
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			outOfSyncApp := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "infra",
						"namespace": "platform-mesh-system",
						"labels": map[string]interface{}{
							"core.platform-mesh.io/operator-created": "true",
						},
					},
					"status": map[string]interface{}{
						"sync": map[string]interface{}{
							"status": "OutOfSync",
						},
						"health": map[string]interface{}{
							"status": "Degraded",
						},
					},
				},
			}
			unstructuredList.Items = []unstructured.Unstructured{outOfSyncApp}
			return nil
		}).Once()

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().NotNil(err)
	s.Assert().Equal(ctrl.Result{}, result)
	s.Assert().Contains(err.Err().Error(), "is not ready yet")
}

func (s *WaitTestSuite) TestProcess_ArgoCD_Application_ByName_Synced() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, s.cfg)

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						Versions:  []string{"v1alpha1"},
						Group:     "argoproj.io",
						Kind:      "Application",
						Namespace: "platform-mesh-system",
						Name:      "infra",
						// Use StatusFieldPath for ArgoCD Application sync status
						StatusFieldPath: []string{"status", "sync", "status"},
						StatusValue:     "Synced",
					},
				},
			},
		},
	}

	// Mock Get call returning synced ArgoCD Application
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Namespace: "platform-mesh-system", Name: "infra"}, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstructuredObj := obj.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"apiVersion": "argoproj.io/v1alpha1",
				"kind":       "Application",
				"metadata": map[string]interface{}{
					"name":      "infra",
					"namespace": "platform-mesh-system",
					"labels": map[string]interface{}{
						"core.platform-mesh.io/operator-created": "true",
					},
				},
				"status": map[string]interface{}{
					"sync": map[string]interface{}{
						"status": "Synced",
					},
					"health": map[string]interface{}{
						"status": "Healthy",
					},
				},
			}
			return nil
		})

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}
