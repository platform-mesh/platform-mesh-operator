package resource_test

import (
	"context"
	"errors"
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
	s.subroutine = subroutines.NewResourceSubroutine(new(mocks.Client))
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
	s.subroutine = subroutines.NewResourceSubroutine(clientMock)

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_GetName() {
	s.Equal("ResourceSubroutine", s.subroutine.GetName())
}

func (s *ResourceTestSuite) Test_Finalize() {
	ctx := context.TODO()
	inst := &unstructured.Unstructured{}
	result, err := s.subroutine.Finalize(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_Finalizers() {
	inst := &unstructured.Unstructured{}
	finalizers := s.subroutine.Finalizers(inst)
	s.Empty(finalizers)
}

func (s *ResourceTestSuite) Test_updateHelmReleaseWithImageTag() {
	tests := []struct {
		name                  string
		repo                  string
		artifact              string
		forAnnotation         string
		pathAnnotation        string
		versionPathAnnotation string
		resourceName          string
		resourceNs            string
		version               string
		versionPath           []string
		expectedName          string
		expectedNs            string
		expectedPath          []string
		expectedVersion       string
	}{
		{
			name:                  "helm image without annotations",
			repo:                  "helm",
			artifact:              "image",
			forAnnotation:         "",
			pathAnnotation:        "",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "1.2.3",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "test-resource",
			expectedNs:            "default",
			expectedPath:          []string{"spec", "values", "image", "tag"},
			expectedVersion:       "1.2.3",
		},
		{
			name:                  "oci image without annotations",
			repo:                  "oci",
			artifact:              "image",
			forAnnotation:         "",
			pathAnnotation:        "",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "2.0.0",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "test-resource",
			expectedNs:            "default",
			expectedPath:          []string{"spec", "values", "image", "tag"},
			expectedVersion:       "2.0.0",
		},
		{
			name:                  "helm image with for annotation - name only",
			repo:                  "helm",
			artifact:              "image",
			forAnnotation:         "target-release",
			pathAnnotation:        "",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "1.2.3",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "target-release",
			expectedNs:            "default",
			expectedPath:          []string{"spec", "values", "image", "tag"},
			expectedVersion:       "1.2.3",
		},
		{
			name:                  "helm image with for annotation - namespace and name",
			repo:                  "helm",
			artifact:              "image",
			forAnnotation:         "target-namespace/target-release",
			pathAnnotation:        "",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "1.2.3",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "target-release",
			expectedNs:            "target-namespace",
			expectedPath:          []string{"spec", "values", "image", "tag"},
			expectedVersion:       "1.2.3",
		},
		{
			name:                  "oci image with custom path annotation",
			repo:                  "oci",
			artifact:              "image",
			forAnnotation:         "",
			pathAnnotation:        "container.imageTag",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "2.0.0",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "test-resource",
			expectedNs:            "default",
			expectedPath:          []string{"spec", "values", "container", "imageTag"},
			expectedVersion:       "2.0.0",
		},
		{
			name:                  "helm image with both for and path annotations",
			repo:                  "helm",
			artifact:              "image",
			forAnnotation:         "other-namespace/other-release",
			pathAnnotation:        "app.version",
			versionPathAnnotation: "",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "3.0.0",
			versionPath:           []string{"status", "resource", "version"},
			expectedName:          "other-release",
			expectedNs:            "other-namespace",
			expectedPath:          []string{"spec", "values", "app", "version"},
			expectedVersion:       "3.0.0",
		},
		{
			name:                  "helm image with custom version-path annotation",
			repo:                  "helm",
			artifact:              "image",
			forAnnotation:         "",
			pathAnnotation:        "",
			versionPathAnnotation: "spec.imageVersion",
			resourceName:          "test-resource",
			resourceNs:            "default",
			version:               "4.5.6",
			versionPath:           []string{"spec", "imageVersion"},
			expectedName:          "test-resource",
			expectedNs:            "default",
			expectedPath:          []string{"spec", "values", "image", "tag"},
			expectedVersion:       "4.5.6",
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
			if tt.versionPathAnnotation != "" {
				annotations["version-path"] = tt.versionPathAnnotation
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

			if err := unstructured.SetNestedField(inst.Object, tt.version, tt.versionPath...); err != nil {
				s.Require().NoError(err)
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

func (s *ResourceTestSuite) Test_updateGitRepo() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-git-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "git",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"access": map[string]interface{}{
						"type":    "gitHub",
						"commit":  "abc123def456",
						"repoUrl": "https://github.com/example/repo.git",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			gitRepo := obj.(*unstructured.Unstructured)

			commit, found, err := unstructured.NestedString(gitRepo.Object, "spec", "ref", "commit")
			s.Require().NoError(err)
			s.Require().True(found)
			s.Require().Equal("abc123def456", commit)

			url, found, err := unstructured.NestedString(gitRepo.Object, "spec", "url")
			s.Require().NoError(err)
			s.Require().True(found)
			s.Require().Equal("https://github.com/example/repo.git", url)

			interval, found, err := unstructured.NestedString(gitRepo.Object, "spec", "interval")
			s.Require().NoError(err)
			s.Require().True(found)
			s.Require().Equal("1m0s", interval)

			return nil
		},
	)

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateGitRepo_CreateOrUpdateError() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-git-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "git",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"access": map[string]interface{}{
						"type":    "gitHub",
						"commit":  "abc123def456",
						"repoUrl": "https://github.com/example/repo.git",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("client error"))

	result, err := s.subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmRepository() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
					"access": map[string]interface{}{
						"type":           "helmChart",
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	getCallCount := 0
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			getCallCount++
			return nil
		},
	)

	updateCallCount := 0
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			updateCallCount++
			unstr := obj.(*unstructured.Unstructured)

			if unstr.GetKind() == "HelmRepository" {
				url, found, err := unstructured.NestedString(unstr.Object, "spec", "url")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("https://charts.example.com", url)

				provider, found, err := unstructured.NestedString(unstr.Object, "spec", "provider")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("generic", provider)

				interval, found, err := unstructured.NestedString(unstr.Object, "spec", "interval")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("5m", interval)
			} else if unstr.GetKind() == "HelmRelease" {
				version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("1.2.3", version)
			}

			return nil
		},
	)

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
	s.Equal(2, getCallCount)
	s.Equal(2, updateCallCount)
}

func (s *ResourceTestSuite) Test_updateHelmRepository_MissingURL() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
					"access":  map[string]interface{}{},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	result, err := s.subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmRelease() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.5.0",
					"access": map[string]interface{}{
						"type":           "helmChart",
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	managerMock := new(mocks.Manager)
	subroutine := subroutines.NewResourceSubroutine(managerMock)
	clientMock := new(mocks.Client)
	managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
			unstr := obj.(*unstructured.Unstructured)

			if unstr.GetKind() == "HelmRepository" {
				return nil
			}
			if unstr.GetKind() == "HelmRelease" {
				version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("2.5.0", version)
			}
			return nil
		},
	).Times(2)

	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmRelease_GetError() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.5.0",
					"access": map[string]interface{}{
						"type":           "helmChart",
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	managerMock := new(mocks.Manager)
	subroutine := subroutines.NewResourceSubroutine(managerMock)
	clientMock := new(mocks.Client)
	managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get error")).Times(1)

	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmRelease_UpdateError() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.5.0",
					"access": map[string]interface{}{
						"type":           "helmChart",
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	managerMock := new(mocks.Manager)
	subroutine := subroutines.NewResourceSubroutine(managerMock)
	clientMock := new(mocks.Client)
	managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).Return(errors.New("update error")).Times(1)

	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmReleaseWithImageTag_GetError() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "image",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	managerMock := new(mocks.Manager)
	subroutine := subroutines.NewResourceSubroutine(managerMock)
	clientMock := new(mocks.Client)
	managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get error"))

	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateHelmReleaseWithImageTag_UpdateError() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "image",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	managerMock := new(mocks.Manager)
	subroutine := subroutines.NewResourceSubroutine(managerMock)
	clientMock := new(mocks.Client)
	managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything).Return(errors.New("update error"))

	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateOciRepo_ParseRefError() {
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
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"type":           "ociArtifact",
						"imageReference": "oci://invalid url with spaces",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	result, err := s.subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateOciRepo_CreateOrUpdateError() {
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
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"type":           "ociArtifact",
						"imageReference": "oci://registry.example.com/charts/mychart:1.0.0",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	s.managerMock.On("GetClient").Return(clientMock)

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("client error"))

	result, err := s.subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_Process_NoAnnotations() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
			},
			"spec": map[string]interface{}{},
		},
	}

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}
