package subroutines

import (
	"context"
	"errors"
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

type DeploymentHelpersTestSuite struct {
	suite.Suite
	log *logger.Logger
}

func TestDeploymentHelpersTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentHelpersTestSuite))
}

func (s *DeploymentHelpersTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "DeploymentHelpersTestSuite"
	var err error
	s.log, err = logger.New(cfg)
	s.Require().NoError(err)
}

func (s *DeploymentHelpersTestSuite) Test_updateObjectMetadata() {
	tests := []struct {
		name     string
		existing *unstructured.Unstructured
		desired  *unstructured.Unstructured
		validate func(*unstructured.Unstructured)
	}{
		{
			name: "update labels and annotations",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels":      map[string]interface{}{"existing": "label"},
						"annotations": map[string]interface{}{"existing": "annotation"},
					},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels":      map[string]interface{}{"desired": "label"},
						"annotations": map[string]interface{}{"desired": "annotation"},
					},
				},
			},
			validate: func(obj *unstructured.Unstructured) {
				labels := obj.GetLabels()
				s.Equal("label", labels["desired"])
				s.NotContains(labels, "existing")

				annotations := obj.GetAnnotations()
				s.Equal("annotation", annotations["desired"])
				s.NotContains(annotations, "existing")
			},
		},
		{
			name: "desired has no metadata",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{"existing": "label"},
					},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			validate: func(obj *unstructured.Unstructured) {
				// Existing labels should remain if desired has none
				labels := obj.GetLabels()
				if labels != nil {
					s.Equal("label", labels["existing"])
				}
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			updateObjectMetadata(tt.existing, tt.desired)
			if tt.validate != nil {
				tt.validate(tt.existing)
			}
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_getOrCreateObject_Existing() {
	ctx := context.Background()
	clientMock := new(mocks.Client)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.group",
		Version: "v1",
		Kind:    "TestResource",
	})
	obj.SetName("test-resource")
	obj.SetNamespace("test-namespace")

	clientMock.EXPECT().Get(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(nil).Run(func(ctx context.Context, key client.ObjectKey, target client.Object, opts ...client.GetOption) {
		// Simulate setting the existing object
		u := target.(*unstructured.Unstructured)
		u.Object = map[string]interface{}{
			"spec": map[string]interface{}{"key": "value"},
		}
	})

	result, err := getOrCreateObject(ctx, clientMock, obj)
	s.NoError(err)
	s.NotNil(result)
	s.Equal("value", result.Object["spec"].(map[string]interface{})["key"])
}

func (s *DeploymentHelpersTestSuite) Test_getOrCreateObject_NotFound() {
	ctx := context.Background()
	clientMock := new(mocks.Client)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.group",
		Version: "v1",
		Kind:    "TestResource",
	})
	obj.SetName("test-resource")
	obj.SetNamespace("test-namespace")
	obj.Object = map[string]interface{}{
		"spec": map[string]interface{}{"key": "value"},
	}

	notFoundErr := kerrors.NewNotFound(schema.GroupResource{
		Group:    "test.group",
		Resource: "testresources",
	}, "test-resource")

	clientMock.EXPECT().Get(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(notFoundErr)

	clientMock.EXPECT().Patch(
		ctx,
		obj,
		client.Apply,
		client.FieldOwner(fieldManagerDeployment),
	).Return(nil)

	result, err := getOrCreateObject(ctx, clientMock, obj)
	s.NoError(err)
	s.NotNil(result)
	s.Equal(obj, result) // Should return the same object that was created
}

func (s *DeploymentHelpersTestSuite) Test_getOrCreateObject_GetError() {
	ctx := context.Background()
	clientMock := new(mocks.Client)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "test.group",
		Version: "v1",
		Kind:    "TestResource",
	})
	obj.SetName("test-resource")
	obj.SetNamespace("test-namespace")

	clientMock.EXPECT().Get(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(errors.New("some error"))

	result, err := getOrCreateObject(ctx, clientMock, obj)
	s.Error(err)
	s.Nil(result)
	s.Contains(err.Error(), "Failed to get existing object")
}
