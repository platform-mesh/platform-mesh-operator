package resource

import (
	"context"
	"errors"
	"testing"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ResourceTestSuite struct {
	suite.Suite
	subroutine *ResourceSubroutine
	clientMock *mocks.Client
}

func TestResourceTestSuite(t *testing.T) {
	suite.Run(t, new(ResourceTestSuite))
}

func (s *ResourceTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.subroutine = NewResourceSubroutine(s.clientMock, nil, nil)
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
					"version": "25.2.3",
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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return false
		}
		url, _, _ := unstructured.NestedString(unstr.Object, "spec", "url")
		return url == "oci://oci-registry-docker-registry.registry.svc.cluster.local/platform-mesh/upstream-images/charts/keycloak"
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
			subroutine := NewResourceSubroutine(clientMock, nil, nil)

			clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
			clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
				func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					unstr := obj.(*unstructured.Unstructured)
					unstr.SetName(key.Name)
					unstr.SetNamespace(key.Namespace)
					unstr.Object["spec"] = map[string]interface{}{"values": map[string]interface{}{}}
					return nil
				},
			)
			clientMock.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
				helmRelease, ok := obj.(*unstructured.Unstructured)
				if !ok {
					return false
				}
				if helmRelease.GetName() != tt.expectedName {
					return false
				}
				if helmRelease.GetNamespace() != tt.expectedNs {
					return false
				}
				actualVersion, found, err := unstructured.NestedString(helmRelease.Object, tt.expectedPath...)
				if err != nil || !found {
					return false
				}
				return actualVersion == tt.expectedVersion
			}), mock.Anything).Return(nil)

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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("client error"))

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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr := obj.(*unstructured.Unstructured)
		if unstr.GetKind() != "HelmRepository" {
			return false
		}
		url, found, err := unstructured.NestedString(unstr.Object, "spec", "url")
		if err != nil || !found || url != "https://charts.example.com" {
			return false
		}
		provider, found, err := unstructured.NestedString(unstr.Object, "spec", "provider")
		if err != nil || !found || provider != "generic" {
			return false
		}
		interval, found, err := unstructured.NestedString(unstr.Object, "spec", "interval")
		if err != nil || !found || interval != "5m" {
			return false
		}
		return true
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			unstr.Object["spec"] = map[string]interface{}{"chart": map[string]interface{}{"spec": map[string]interface{}{}}}
			return nil
		},
	).Times(1)
	clientMock.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr := obj.(*unstructured.Unstructured)
		version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
		return err == nil && found && version == "1.2.3"
	}), mock.Anything).Return(nil).Times(1)

	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	result, err := s.subroutine.Process(ctx, inst)
	s.Nil(err)
	s.True(result.IsStopWithRequeue())
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

	clientMock := new(mocks.Client)
	subroutine := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr := obj.(*unstructured.Unstructured)
		return unstr.GetKind() == "HelmRepository"
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			unstr.Object["spec"] = map[string]interface{}{"chart": map[string]interface{}{"spec": map[string]interface{}{}}}
			return nil
		},
	).Times(1)
	clientMock.EXPECT().Update(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr := obj.(*unstructured.Unstructured)
		version, found, err := unstructured.NestedString(unstr.Object, "spec", "chart", "spec", "version")
		return err == nil && found && version == "2.5.0"
	}), mock.Anything).Return(nil).Times(1)

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

	clientMock := new(mocks.Client)
	subroutine := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get error")).Times(1)

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

	clientMock := new(mocks.Client)
	subroutine := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			unstr.Object["spec"] = map[string]interface{}{"chart": map[string]interface{}{"spec": map[string]interface{}{}}}
			return nil
		},
	).Times(1)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update error")).Times(1)

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

	clientMock := new(mocks.Client)
	subroutine := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("get error"))

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

	clientMock := new(mocks.Client)
	subroutine := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			unstr.Object["spec"] = map[string]interface{}{"values": map[string]interface{}{}}
			return nil
		},
	)
	clientMock.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update error"))

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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
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
	s.subroutine = NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("client error"))

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

func (s *ResourceTestSuite) Test_updateArgoCDApplication_HelmRepo() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":      "keycloak-chart",
				"namespace": "platform-mesh-system",
				"annotations": map[string]interface{}{
					"artifact": "chart",
					"repo":     "helm",
				},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "25.2.3",
					"access": map[string]interface{}{
						"type":           "helmChart",
						"helmRepository": "https://charts.bitnami.com/bitnami",
					},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	sub := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("no CRD")).Maybe()
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "platform-mesh-profile" || key.Name == "platform-mesh-system-profile" {
				cm := obj.(*corev1.ConfigMap)
				cm.Data = map[string]string{"profile.yaml": "infra:\n  deploymentTechnology: argocd\n"}
				return nil
			}
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			_ = unstructured.SetNestedField(unstr.Object, "https://old-repo.com", "spec", "source", "repoURL")
			_ = unstructured.SetNestedField(unstr.Object, "1.0.0", "spec", "source", "targetRevision")
			return nil
		},
	)
	clientMock.EXPECT().Patch(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		unstr := obj.(*unstructured.Unstructured)
		repoURL, _, _ := unstructured.NestedString(unstr.Object, "spec", "source", "repoURL")
		rev, _, _ := unstructured.NestedString(unstr.Object, "spec", "source", "targetRevision")
		return repoURL == "https://charts.bitnami.com/bitnami" && rev == "25.2.3" && unstr.GetName() == "keycloak"
	}), mock.Anything, mock.Anything, mock.Anything).Return(nil)

	result, err := sub.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateArgoCDApplication_AlreadyUpToDate() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":        "keycloak-chart",
				"namespace":   "platform-mesh-system",
				"annotations": map[string]interface{}{"artifact": "chart", "repo": "helm"},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "25.2.3",
					"access":  map[string]interface{}{"type": "helmChart", "helmRepository": "https://charts.bitnami.com/bitnami"},
				},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	sub := NewResourceSubroutine(clientMock, nil, nil)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("no CRD")).Maybe()
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "platform-mesh-profile" || key.Name == "platform-mesh-system-profile" {
				cm := obj.(*corev1.ConfigMap)
				cm.Data = map[string]string{"profile.yaml": "infra:\n  deploymentTechnology: argocd\n"}
				return nil
			}
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			_ = unstructured.SetNestedField(unstr.Object, "https://charts.bitnami.com/bitnami", "spec", "source", "repoURL")
			_ = unstructured.SetNestedField(unstr.Object, "25.2.3", "spec", "source", "targetRevision")
			return nil
		},
	)

	result, err := sub.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)
}

func (s *ResourceTestSuite) Test_updateArgoCDApplicationHelmValues() {
	ctx := context.TODO()

	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "delivery.ocm.software/v1alpha1",
			"kind":       "Resource",
			"metadata": map[string]interface{}{
				"name":        "kcp-image",
				"namespace":   "platform-mesh-system",
				"annotations": map[string]interface{}{"artifact": "image", "repo": "oci", "path": "kcp.image.tag"},
			},
			"status": map[string]interface{}{
				"resource": map[string]interface{}{"version": "v0.30.0"},
			},
			"spec": map[string]interface{}{},
		},
	}

	clientMock := new(mocks.Client)
	store := subroutines.NewImageVersionStore()
	sub := NewResourceSubroutine(clientMock, nil, store)

	clientMock.On("List", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("no CRD")).Maybe()
	clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "platform-mesh-profile" || key.Name == "platform-mesh-system-profile" {
				cm := obj.(*corev1.ConfigMap)
				cm.Data = map[string]string{"profile.yaml": "infra:\n  deploymentTechnology: argocd\n"}
				return nil
			}
			unstr := obj.(*unstructured.Unstructured)
			unstr.SetName(key.Name)
			unstr.SetNamespace(key.Namespace)
			_ = unstructured.SetNestedField(unstr.Object, "kcp:\n  image:\n    tag: v0.29.0\n", "spec", "source", "helm", "values")
			return nil
		},
	)
	clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	result, err := sub.Process(ctx, inst)
	s.Nil(err)
	s.NotNil(result)

	versions := store.Get("platform-mesh-system", "kcp")
	s.Require().Len(versions, 1)
	s.Equal("kcp.image.tag", versions[0].Path)
	s.Equal("v0.30.0", versions[0].Version)
}

func (s *ResourceTestSuite) Test_resolveArgoCDSource_OCI() {
	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"resource": map[string]interface{}{
					"version": "1.2.3",
					"access":  map[string]interface{}{"imageReference": "oci://registry.example.com/charts/mychart:1.2.3@sha256:abc"},
				},
			},
		},
	}
	repoURL, rev, chartType, err := s.subroutine.resolveArgoCDSource(inst)
	s.Nil(err)
	s.Equal("registry.example.com/charts", repoURL)
	s.Equal("1.2.3", rev)
	s.Equal("oci", chartType)
}

func (s *ResourceTestSuite) Test_resolveArgoCDSource_NoSource() {
	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{"status": map[string]interface{}{"resource": map[string]interface{}{"access": map[string]interface{}{}}}},
	}
	_, _, _, err := s.subroutine.resolveArgoCDSource(inst)
	s.NotNil(err)
	s.Contains(err.Error(), "no helmRepository, repoUrl, or imageReference found")
}

func (s *ResourceTestSuite) Test_resolveArgoCDSource_HelmNoVersion() {
	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{"status": map[string]interface{}{"resource": map[string]interface{}{"access": map[string]interface{}{"helmRepository": "https://charts.example.com"}}}},
	}
	_, _, _, err := s.subroutine.resolveArgoCDSource(inst)
	s.NotNil(err)
	s.Contains(err.Error(), "version not found for helm chart")
}

func (s *ResourceTestSuite) Test_resolveArgoCDSource_GitNoRef() {
	inst := &unstructured.Unstructured{
		Object: map[string]interface{}{"status": map[string]interface{}{"resource": map[string]interface{}{"access": map[string]interface{}{"repoUrl": "https://github.com/org/repo"}}}},
	}
	_, _, _, err := s.subroutine.resolveArgoCDSource(inst)
	s.NotNil(err)
	s.Contains(err.Error(), "no ref, version, or commit found")
}

func Test_extractOCIRepoURL(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"oci://registry.example.com/charts/mychart:1.0.0@sha256:abc", "registry.example.com/charts", false},
		{"registry.example.com/org/charts/app:v2.0", "registry.example.com/org/charts", false},
		{"noslash", "", true},
	}
	for _, tt := range tests {
		got, err := extractOCIRepoURL(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("extractOCIRepoURL(%q) expected error", tt.input)
			}
		} else {
			if err != nil {
				t.Fatalf("extractOCIRepoURL(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("extractOCIRepoURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

func Test_firstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c"); got != "c" {
		t.Errorf("got %q want %q", got, "c")
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("got %q want %q", got, "a")
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q want %q", got, "")
	}
}

func Test_getValueFromYAML(t *testing.T) {
	yamlStr := "kcp:\n  image:\n    tag: v0.30.0\n"
	if got := getValueFromYAML(yamlStr, []string{"kcp", "image", "tag"}); got != "v0.30.0" {
		t.Errorf("got %q want %q", got, "v0.30.0")
	}
	if got := getValueFromYAML(yamlStr, []string{"missing"}); got != "" {
		t.Errorf("got %q want empty", got)
	}
	if got := getValueFromYAML("", []string{"a"}); got != "" {
		t.Errorf("got %q want empty", got)
	}
}

func Test_getNestedString(t *testing.T) {
	m := map[string]interface{}{"a": map[string]interface{}{"b": "hello"}}
	got, ok := getNestedString(m, "a", "b")
	if !ok || got != "hello" {
		t.Errorf("got %q, ok=%v", got, ok)
	}
	_, ok = getNestedString(m, "x", "y")
	if ok {
		t.Error("expected ok=false")
	}
	_, ok = getNestedString(m)
	if ok {
		t.Error("expected ok=false for empty path")
	}
	m2 := map[string]interface{}{"a": 42}
	_, ok = getNestedString(m2, "a")
	if ok {
		t.Error("expected ok=false for non-string")
	}
}

func (s *ResourceTestSuite) Test_SetRuntimeClient() {
	clientMock := new(mocks.Client)
	sub := NewResourceSubroutine(s.clientMock, nil, nil)
	sub.SetRuntimeClient(clientMock)
	s.Equal(clientMock, sub.clientRuntime)
}
