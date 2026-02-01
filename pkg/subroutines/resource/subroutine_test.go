package resource_test

import (
	"context"
	"errors"
	"testing"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
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
	s.subroutine = subroutines.NewResourceSubroutine(new(mocks.Client), &config.OperatorConfig{})
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
			clientMock := new(mocks.Client)
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

			managerMock.On("GetClient").Return(clientMock)
			subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

			// updateHelmReleaseWithImageTag uses Patch (Server-Side Apply) directly without Get
			// Patch is called with: ctx, obj, patch, fieldOwner, forceOwnership (5 args total)
			clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
				func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
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

	// updateGitRepo uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
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

	sub := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := sub.Process(ctx, inst)

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

	// updateGitRepo uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error"))

	sub := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := sub.Process(ctx, inst)

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

	// updateHelmRepository uses Patch (Server-Side Apply) directly without Get
	patchCallCount := 0
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			patchCallCount++
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
			}

			return nil
		},
	)

	// HelmRelease now uses Patch (Server-Side Apply) instead of Update
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			unstr := obj.(*unstructured.Unstructured)
			if unstr.GetKind() == "HelmRelease" {
				version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("1.2.3", version)
			}
			return nil
		},
	)

	sub := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := sub.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
	// updateHelmRepository: 1 Patch (Server-Side Apply, no Get needed)
	// updateHelmRelease: 1 Patch (Server-Side Apply, no Get needed)
	s.Equal(2, patchCallCount) // One for HelmRepository, one for HelmRelease
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
	clientMock := new(mocks.Client)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	managerMock.On("GetClient").Return(clientMock)

	// updateHelmRepository uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			unstr := obj.(*unstructured.Unstructured)
			if unstr.GetKind() == "HelmRepository" {
				return nil
			}
			return nil
		},
	)
	// updateHelmRelease uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			unstr := obj.(*unstructured.Unstructured)
			if unstr.GetKind() == "HelmRelease" {
				version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
				s.Require().NoError(err)
				s.Require().True(found)
				s.Require().Equal("2.5.0", version)
			}
			return nil
		},
	)

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
	clientMock := new(mocks.Client)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	managerMock.On("GetClient").Return(clientMock)

	// updateHelmRepository uses Patch (Server-Side Apply) directly without Get
	// updateHelmRelease uses Patch directly, so test Patch error
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()                       // HelmRepository
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error")).Once() // HelmRelease

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
	clientMock := new(mocks.Client)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	managerMock.On("GetClient").Return(clientMock)

	// updateHelmRepository uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once() // HelmRepository
	// updateHelmRelease uses Patch (Server-Side Apply) directly
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error")).Once() // HelmRelease

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
	clientMock := new(mocks.Client)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	managerMock.On("GetClient").Return(clientMock)

	// updateHelmReleaseWithImageTag uses Patch directly, so test Patch error
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error"))

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
	clientMock := new(mocks.Client)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	managerMock.On("GetClient").Return(clientMock)

	// updateHelmReleaseWithImageTag uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error"))

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
	// updateOciRepo uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error"))
	s.managerMock.On("GetClient").Return(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
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
