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
				"annotations": map[string]interface{}{
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

func (s *ResourceTestSuite) Test_updateHelmReleaseWithImageTag() {
	tests := []struct {
		name            string
		repo            string
		artifact        string
		forAnnotation   string
		pathAnnotation  string
		resourceName    string
		resourceNs      string
		version         string
		expectedName    string
		expectedNs      string
		expectedPath    []string
		expectedVersion string
	}{
		{
			name:            "helm image without annotations",
			repo:            "helm",
			artifact:        "image",
			forAnnotation:   "",
			pathAnnotation:  "",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "1.2.3",
			expectedName:    "test-resource",
			expectedNs:      "default",
			expectedPath:    []string{"spec", "values", "image", "tag"},
			expectedVersion: "1.2.3",
		},
		{
			name:            "oci image without annotations",
			repo:            "oci",
			artifact:        "image",
			forAnnotation:   "",
			pathAnnotation:  "",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "2.0.0",
			expectedName:    "test-resource",
			expectedNs:      "default",
			expectedPath:    []string{"spec", "values", "image", "tag"},
			expectedVersion: "2.0.0",
		},
		{
			name:            "helm image with for annotation - name only",
			repo:            "helm",
			artifact:        "image",
			forAnnotation:   "target-release",
			pathAnnotation:  "",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "1.2.3",
			expectedName:    "target-release",
			expectedNs:      "default",
			expectedPath:    []string{"spec", "values", "image", "tag"},
			expectedVersion: "1.2.3",
		},
		{
			name:            "helm image with for annotation - namespace and name",
			repo:            "helm",
			artifact:        "image",
			forAnnotation:   "target-namespace/target-release",
			pathAnnotation:  "",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "1.2.3",
			expectedName:    "target-release",
			expectedNs:      "target-namespace",
			expectedPath:    []string{"spec", "values", "image", "tag"},
			expectedVersion: "1.2.3",
		},
		{
			name:            "oci image with custom path annotation",
			repo:            "oci",
			artifact:        "image",
			forAnnotation:   "",
			pathAnnotation:  "container.imageTag",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "2.0.0",
			expectedName:    "test-resource",
			expectedNs:      "default",
			expectedPath:    []string{"spec", "values", "container", "imageTag"},
			expectedVersion: "2.0.0",
		},
		{
			name:            "helm image with both for and path annotations",
			repo:            "helm",
			artifact:        "image",
			forAnnotation:   "other-namespace/other-release",
			pathAnnotation:  "app.version",
			resourceName:    "test-resource",
			resourceNs:      "default",
			version:         "3.0.0",
			expectedName:    "other-release",
			expectedNs:      "other-namespace",
			expectedPath:    []string{"spec", "values", "app", "version"},
			expectedVersion: "3.0.0",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			managerMock := new(mocks.Manager)
			subroutine := subroutines.NewResourceSubroutine(managerMock)
			ctx := context.TODO()

			annotations := map[string]interface{}{
				"artifact": tt.artifact,
				"repo":     tt.repo,
			}
			if tt.forAnnotation != "" {
				annotations["for"] = tt.forAnnotation
			}
			if tt.pathAnnotation != "" {
				annotations["path"] = tt.pathAnnotation
			}

			inst := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "delivery.ocm.software/v1alpha1",
					"kind":       "Resource",
					"metadata": map[string]interface{}{
						"name":        tt.resourceName,
						"namespace":   tt.resourceNs,
						"annotations": annotations,
					},
					"status": map[string]interface{}{
						"resource": map[string]interface{}{
							"version": tt.version,
						},
					},
					"spec": map[string]interface{}{},
				},
			}

			clientMock := new(mocks.Client)
			managerMock.On("GetClient").Return(clientMock)

			clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
				func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					s.Require().Equal(tt.expectedName, key.Name)
					s.Require().Equal(tt.expectedNs, key.Namespace)
					return nil
				},
			)

			clientMock.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
				func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					helmRelease := obj.(*unstructured.Unstructured)

					actualVersion, found, err := unstructured.NestedString(helmRelease.Object, tt.expectedPath...)
					s.Require().NoError(err)
					s.Require().True(found)
					s.Require().Equal(tt.expectedVersion, actualVersion)
					return nil
				},
			)

			result, err := subroutine.Process(ctx, inst)
			s.Nil(err)
			s.NotNil(result)
		})
	}
}
