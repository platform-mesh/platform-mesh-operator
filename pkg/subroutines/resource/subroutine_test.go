package resource_test

import (
	"context"
	"testing"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	subroutines "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/resource"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ResourceTestSuite struct {
	suite.Suite
	subroutine  *subroutines.ResourceSubroutine
	managerMock *mocks.Manager
}

func TestResourceTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceTestSuite))
}

func (s *ResourceTestSuite) SetupTest() {
	s.managerMock = new(mocks.Manager)
	s.subroutine = subroutines.NewResourceSubroutine(s.managerMock)
}

func (s *ResourceTestSuite) Test_applyReleaseWithValues() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				"labels": map[string]interface{}{
					"artifact": "chart",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
				"resource": map[string]interface{}{
					"access": map[string]interface{}{
						"type":           "ociArtifact",
						"imageReference": "oci://oci-registry-docker-registry.registry.svc.cluster.local/platform-mesh/upstream-images/charts/keycloak:25.2.3@sha256:cb5be99827d7cfa107fc7ca06f5b2fb0ea486f3ffb0315baf2be1bb348f9db77",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	clientMock.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			// Simulate a successful patch operation
			ocirepo := obj.(*unstructured.Unstructured)

			spec, _, _ := unstructured.NestedFieldNoCopy(ocirepo.Object, "spec")
			url := spec.(map[string]interface{})["url"]
			s.Require().Equal(url, "oci://oci-registry-docker-registry.registry.svc.cluster.local/platform-mesh/upstream-images/charts/keycloak")
			return nil
		},
	)

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}
