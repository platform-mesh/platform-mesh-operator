package subroutines_test

import (
	"context"
	"errors"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WaitTestSuite struct {
	suite.Suite
	clientMock    *mocks.Client
	kcpClientMock *mocks.Client
	kcpHelperMock *mocks.KcpHelper
	testObj       *subroutines.WaitSubroutine
	log           *logger.Logger
	cfg           config.OperatorConfig
}

func TestWaitTestSuite(t *testing.T) {
	suite.Run(t, new(WaitTestSuite))
}

func (s *WaitTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
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
	s.testObj = subroutines.NewWaitSubroutine(s.clientMock, s.kcpHelperMock, &s.cfg, "https://kcp.example.com")
}

func (s *WaitTestSuite) TearDownTest() {
	s.clientMock = nil
	s.kcpClientMock = nil
	s.kcpHelperMock = nil
	s.testObj = nil
}

func (s *WaitTestSuite) mockWorkspaceAuthConfigCheck(audience string) {
	s.clientMock.EXPECT().
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
		}).Twice()

	s.mockWorkspaceAuthConfigCheck("valid-audience")

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestProcess_AllResourcesReady() {
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
						"namespace": "default",
					},
					"status": map[string]any{
						"conditions": []any{
							map[string]any{"type": "Ready", "status": "True"},
						},
					},
				},
			}}
			return nil
		}).Twice()

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
						"namespace": "default",
						"labels": map[string]interface{}{
							"helm.toolkit.fluxcd.io/name": "platform-mesh-operator-components",
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
					"metadata":   map[string]any{"name": "test", "namespace": "default"},
					"status": map[string]any{
						"conditions": []any{
							map[string]any{"type": "Ready", "status": "True"},
						},
					},
				},
			}}
			return nil
		}).Twice()

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

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						APIVersions:     metav1.APIVersions{Versions: []string{"v1"}},
						GroupKind:       metav1.GroupKind{Group: "kustomize.toolkit.fluxcd.io", Kind: "Kustomization"},
						Namespace:       "flux-system",
						LabelSelector:   metav1.LabelSelector{MatchLabels: map[string]string{"app": "platform-mesh"}},
						ConditionStatus: metav1.ConditionTrue, RowConditionType: "Ready",
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

	instance := &corev1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-mesh",
			Namespace: "default",
		},
		Spec: corev1alpha1.PlatformMeshSpec{
			Wait: &corev1alpha1.WaitConfig{
				ResourceTypes: []corev1alpha1.ResourceType{
					{
						APIVersions:      metav1.APIVersions{Versions: []string{"v2"}},
						GroupKind:        metav1.GroupKind{Group: "helm.toolkit.fluxcd.io", Kind: "HelmRelease"},
						Namespace:        "default",
						Name:             "platform-mesh-operator-components",
						ConditionStatus:  metav1.ConditionTrue,
						RowConditionType: "Ready",
					},
				},
			},
		},
	}

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "platform-mesh-operator-components"}, mock.Anything).
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
