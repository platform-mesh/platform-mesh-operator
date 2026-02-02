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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

// setupDeploymentTechMocks sets up mock expectations for getDeploymentTechnologyFromProfile
// which is called by Process to determine deployment technology (fluxcd/argocd)
func setupDeploymentTechMocks(clientMock *mocks.Client) {
	// Mock List for PlatformMeshList - returns empty list
	clientMock.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.PlatformMeshList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			// Return empty list, triggering ConfigMap fallback
			return nil
		}).Maybe()
	// Mock Get for ConfigMap lookups - returns not found, defaulting to "fluxcd"
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.ConfigMap")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmaps"}, "")).Maybe()
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
			setupDeploymentTechMocks(clientMock)
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
	setupDeploymentTechMocks(clientMock)

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
	setupDeploymentTechMocks(clientMock)

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
	setupDeploymentTechMocks(clientMock)

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
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
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
	managerMock.On("GetClient").Return(clientMock)
	setupDeploymentTechMocks(clientMock)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

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
	managerMock.On("GetClient").Return(clientMock)
	setupDeploymentTechMocks(clientMock)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

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
	managerMock.On("GetClient").Return(clientMock)
	setupDeploymentTechMocks(clientMock)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

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
	managerMock.On("GetClient").Return(clientMock)
	setupDeploymentTechMocks(clientMock)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

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
	managerMock.On("GetClient").Return(clientMock)
	setupDeploymentTechMocks(clientMock)
	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})

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
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
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
	setupDeploymentTechMocks(clientMock)
	// updateOciRepo uses Patch (Server-Side Apply) directly without Get
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch error"))

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

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test SetRuntimeClient
func (s *ResourceTestSuite) Test_SetRuntimeClient() {
	clientMock := new(mocks.Client)
	runtimeClientMock := new(mocks.Client)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	subroutine.SetRuntimeClient(runtimeClientMock)

	// Verify by running a process that uses the runtime client
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

	// Setup mocks on the runtime client (used for deployment tech lookup)
	runtimeClientMock.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.PlatformMeshList"), mock.Anything).Return(nil).Maybe()
	runtimeClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.ConfigMap")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "")).Maybe()

	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test getMetadataValue with labels fallback
func (s *ResourceTestSuite) Test_Process_WithLabelsOnly() {
	ctx := context.TODO()

	// Resource with labels instead of annotations
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
	setupDeploymentTechMocks(clientMock)
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test updateOciRepo success path
func (s *ResourceTestSuite) Test_updateOciRepo_Success() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-oci-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
					"access": map[string]interface{}{
						"type":           "ociArtifact",
						"imageReference": "oci://ghcr.io/example/charts/mychart:1.2.3",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			ociRepo := obj.(*unstructured.Unstructured)

			s.Equal("OCIRepository", ociRepo.GetKind())
			s.Equal("test-oci-resource", ociRepo.GetName())

			tag, found, err := unstructured.NestedString(ociRepo.Object, "spec", "ref", "tag")
			s.NoError(err)
			s.True(found)
			s.Equal("1.2.3", tag)

			url, found, err := unstructured.NestedString(ociRepo.Object, "spec", "url")
			s.NoError(err)
			s.True(found)
			s.Contains(url, "oci://")

			return nil
		},
	)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test updateOciRepo with missing imageReference
func (s *ResourceTestSuite) Test_updateOciRepo_MissingImageReference() {
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
					"access":  map[string]interface{}{},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

// Test updateGitRepo with missing repoUrl
func (s *ResourceTestSuite) Test_updateGitRepo_MissingRepoUrl() {
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
						"type":   "gitHub",
						"commit": "abc123",
						// Missing repoUrl
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

// Test helm chart with FluxCD - tests the full helm chart processing path
func (s *ResourceTestSuite) Test_FluxCD_HelmChart_FullPath() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-helm-chart-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)

	// Setup mocks - list returns error, triggering ConfigMap fallback
	clientMock.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.PlatformMeshList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("list error")
		}).Maybe()

	// ConfigMap lookups return not found, defaulting to fluxcd
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.ConfigMap")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "")).Maybe()

	// FluxCD path: HelmRepository Patch + HelmRelease Patch
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD image artifact - Application not found
func (s *ResourceTestSuite) Test_ArgoCD_ImageArtifact_ApplicationNotFound() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-image",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "image",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.0.0",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	// For FluxCD path (default), updateHelmReleaseWithImageTag is called
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD with unsupported artifact type
func (s *ResourceTestSuite) Test_Process_ArgoCD_UnsupportedArtifact() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "unsupported",
					"repo":     "other",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	// Should succeed with no action taken (no matching conditions)
	s.Nil(err)
	s.NotNil(result)
}

// Test updateHelmRepository Patch error
func (s *ResourceTestSuite) Test_updateHelmRepository_PatchError() {
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
	setupDeploymentTechMocks(clientMock)

	// HelmRepository Patch fails
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("patch error")).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.NotNil(err)
	s.NotNil(result)
}

// Test with nil labels (covers getMetadataValue nil labels path)
func (s *ResourceTestSuite) Test_Process_NilLabelsAndAnnotations() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				// No annotations, no labels
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test with empty annotations (annotation key exists but value is empty)
func (s *ResourceTestSuite) Test_Process_EmptyAnnotationValues() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "",
					"repo":     "",
				},
				"labels": map[string]interface{}{
					"artifact": "chart",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"imageReference": "oci://registry.example.com/charts/mychart:1.0.0",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupDeploymentTechMocks(clientMock)
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// setupArgoCDDeploymentTechMocks sets up mocks to return argocd deployment technology
func setupArgoCDDeploymentTechMocks(clientMock *mocks.Client) {
	// Mock List for PlatformMeshList - returns empty list
	clientMock.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.PlatformMeshList"), mock.Anything).
		RunAndReturn(func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
			return nil
		}).Maybe()
	// Mock Get for ConfigMap lookups - returns ConfigMap with argocd deployment technology
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.ConfigMap")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "platform-mesh-profile" {
				cm := obj.(*corev1.ConfigMap)
				cm.Name = key.Name
				cm.Namespace = key.Namespace
				cm.Data = map[string]string{
					"profile.yaml": `
infra:
  deploymentTechnology: argocd
`,
				}
				return nil
			}
			return apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmaps"}, key.Name)
		}).Maybe()
}

// Test ArgoCD chart artifact with Helm repository - full path
func (s *ResourceTestSuite) Test_ArgoCD_ChartArtifact_HelmRepository() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-helm-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns existing application
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	existingApp.SetName("test-argocd-helm-resource")
	existingApp.SetNamespace("default")
	_ = unstructured.SetNestedField(existingApp.Object, "https://old-repo.example.com", "spec", "source", "repoURL")
	_ = unstructured.SetNestedField(existingApp.Object, "0.9.0", "spec", "source", "targetRevision")

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			existingApp.DeepCopyInto(unstr)
			return nil
		}).Once()

	// ArgoCD Application Patch
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD chart artifact - Application already up to date
func (s *ResourceTestSuite) Test_ArgoCD_ChartArtifact_AlreadyUpToDate() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns existing application with same values
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	existingApp.SetName("test-argocd-resource")
	existingApp.SetNamespace("default")
	_ = unstructured.SetNestedField(existingApp.Object, "https://charts.example.com", "spec", "source", "repoURL")
	_ = unstructured.SetNestedField(existingApp.Object, "1.0.0", "spec", "source", "targetRevision")

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			existingApp.DeepCopyInto(unstr)
			return nil
		}).Once()

	// No Patch expected since values are already up to date

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD chart artifact - Application not found (skip update)
func (s *ResourceTestSuite) Test_ArgoCD_ChartArtifact_AppNotFound() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns not found
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "test-argocd-resource")).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err) // Should succeed - skipping update is not an error
	s.NotNil(result)
}

// Test ArgoCD image artifact with Helm values update
func (s *ResourceTestSuite) Test_ArgoCD_ImageArtifact_HelmValuesUpdate() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-image-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "image",
					"repo":     "oci",
					"for":      "target-app",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.0.0",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns existing application
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	existingApp.SetName("target-app")
	existingApp.SetNamespace("default")
	_ = unstructured.SetNestedField(existingApp.Object, "image:\n  tag: 1.0.0\n", "spec", "source", "helm", "values")

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			existingApp.DeepCopyInto(unstr)
			return nil
		}).Once()

	// ArgoCD Application Patch for Helm values
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD image artifact - Application not found
func (s *ResourceTestSuite) Test_ArgoCD_ImageArtifact_AppNotFound() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-image",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "image",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "2.0.0",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns not found
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "test-argocd-image")).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err) // Should succeed - skipping update is not an error
	s.NotNil(result)
}

// Test ArgoCD unsupported artifact type
func (s *ResourceTestSuite) Test_ArgoCD_UnsupportedArtifact() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "unknown",
					"repo":     "other",
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err) // Should succeed - unsupported artifact is just skipped
	s.NotNil(result)
}

// Test ArgoCD chart with OCI imageReference
func (s *ResourceTestSuite) Test_ArgoCD_ChartArtifact_OCIImageReference() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-oci-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "oci",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
					"access": map[string]interface{}{
						"imageReference": "ghcr.io/example/charts/mychart:1.2.3",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns existing application
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	existingApp.SetName("test-argocd-oci-resource")
	existingApp.SetNamespace("default")
	_ = unstructured.SetNestedField(existingApp.Object, "ghcr.io/example/charts", "spec", "source", "repoURL")
	_ = unstructured.SetNestedField(existingApp.Object, "1.0.0", "spec", "source", "targetRevision")

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			existingApp.DeepCopyInto(unstr)
			return nil
		}).Once()

	// ArgoCD Application Patch
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ArgoCD chart with Git repoUrl
func (s *ResourceTestSuite) Test_ArgoCD_ChartArtifact_GitRepo() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "test-argocd-git-resource",
				"namespace": "default",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "git",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"repoUrl": "https://github.com/example/charts.git",
						"ref":     "v1.0.0",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	setupArgoCDDeploymentTechMocks(clientMock)

	// ArgoCD Application Get - returns existing application
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})
	existingApp.SetName("test-argocd-git-resource")
	existingApp.SetNamespace("default")
	_ = unstructured.SetNestedField(existingApp.Object, "https://github.com/example/charts.git", "spec", "source", "repoURL")
	_ = unstructured.SetNestedField(existingApp.Object, "v0.9.0", "spec", "source", "targetRevision")

	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			existingApp.DeepCopyInto(unstr)
			return nil
		}).Once()

	// ArgoCD Application Patch
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

// Test ConfigMap with deploymentTechnology in components section
func (s *ResourceTestSuite) Test_DeploymentTech_ComponentsSection() {
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
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.0.0",
					"access": map[string]interface{}{
						"helmRepository": "https://charts.example.com",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)

	// Mock List for PlatformMeshList - returns empty list
	clientMock.EXPECT().List(mock.Anything, mock.AnythingOfType("*v1alpha1.PlatformMeshList"), mock.Anything).Return(nil).Maybe()

	// Mock Get for ConfigMap - first one not found, second one has components section
	callCount := 0
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1.ConfigMap")).
		RunAndReturn(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			callCount++
			if key.Name == "platform-mesh-system-profile" {
				cm := obj.(*corev1.ConfigMap)
				cm.Name = key.Name
				cm.Namespace = key.Namespace
				cm.Data = map[string]string{
					"profile.yaml": `
components:
  deploymentTechnology: argocd
`,
				}
				return nil
			}
			return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
		}).Maybe()

	// ArgoCD Application Get
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "test-resource")).Once()

	subroutine := subroutines.NewResourceSubroutine(clientMock, &config.OperatorConfig{})
	result, err := subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}
