package subroutines_test

import (
	"context"
	"errors"
	"testing"

	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WaitTestSuite struct {
	suite.Suite
	clientMock *mocks.Client
	testObj    *subroutines.WaitSubroutine
	log        *logger.Logger
}

func TestWaitTestSuite(t *testing.T) {
	suite.Run(t, new(WaitTestSuite))
}

func (s *WaitTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "WaitTestSuite"
	s.log, _ = logger.New(cfg)
	s.testObj = subroutines.NewWaitSubroutine(s.clientMock)
}

func (s *WaitTestSuite) TearDownTest() {
	s.clientMock = nil
	s.testObj = nil
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

	// Mock List calls for each resource type in DEFAULT_WAIT_CONFIG
	// For HelmRelease v2 - platform-mesh-operator-components
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			// Return empty list
			return nil
		}).Twice() // Called twice for the two default resource types

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

	// Mock List calls returning ready resources
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
						"namespace": "default",
						"labels": map[string]interface{}{
							"helm.toolkit.fluxcd.io/name": "platform-mesh-operator-components",
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
		}).Twice()

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

	callCount := 0
	// Mock List calls for multiple resource types
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			callCount++

			var helmReleaseName string
			if callCount == 1 {
				helmReleaseName = "platform-mesh-operator-components"
			} else {
				helmReleaseName = "platform-mesh-operator-infra-components"
			}

			readyResource := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "helm.toolkit.fluxcd.io/v2",
					"kind":       "HelmRelease",
					"metadata": map[string]interface{}{
						"name":      helmReleaseName,
						"namespace": "default",
						"labels": map[string]interface{}{
							"helm.toolkit.fluxcd.io/name": helmReleaseName,
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
		}).Twice()

	result, err := s.testObj.Process(ctx, instance)

	s.Assert().Nil(err)
	s.Assert().Equal(ctrl.Result{}, result)
}

func (s *WaitTestSuite) TestFinalizers() {
	finalizers := s.testObj.Finalizers()
	s.Assert().Equal([]string{subroutines.WaitSubroutineFinalizer}, finalizers)
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
						APIVersions: metav1.APIVersions{
							Versions: []string{"v1"},
						},
						GroupKind: metav1.GroupKind{
							Group: "kustomize.toolkit.fluxcd.io",
							Kind:  "Kustomization",
						},
						Namespace: "flux-system",
						LabelSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "platform-mesh",
							},
						},
						ConditionStatus:  metav1.ConditionTrue,
						RowConditionType: "Ready",
					},
				},
			},
		},
	}

	// Mock List call returning ready Kustomization resource
	s.clientMock.EXPECT().
		List(mock.Anything, mock.AnythingOfType("*unstructured.UnstructuredList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			unstructuredList := list.(*unstructured.UnstructuredList)
			readyResource := unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
					"kind":       "Kustomization",
					"metadata": map[string]interface{}{
						"name":      "test-kustomization",
						"namespace": "flux-system",
						"labels": map[string]interface{}{
							"app": "platform-mesh",
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
