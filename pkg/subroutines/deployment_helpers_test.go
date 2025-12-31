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

func (s *DeploymentHelpersTestSuite) Test_mergeHelmReleaseField() {
	tests := []struct {
		name          string
		existingField map[string]interface{}
		desiredField  map[string]interface{}
		fieldName     string
		expectMerged  bool
		expectError   bool
	}{
		{
			name:          "both fields exist - should merge",
			existingField: map[string]interface{}{"existing": "value", "shared": "existing"},
			desiredField:  map[string]interface{}{"desired": "value", "shared": "desired"},
			fieldName:     specFieldValues,
			expectMerged:  true,
			expectError:   false,
		},
		{
			name:          "removes keys not in desired",
			existingField: map[string]interface{}{"a": "value1", "b": "value2", "c": "value3"},
			desiredField:  map[string]interface{}{"a": "value1", "b": "value2"},
			fieldName:     specFieldValues,
			expectMerged:  true,
			expectError:   false,
		},
		{
			name: "removes nested keys not in desired",
			existingField: map[string]interface{}{
				"log": map[string]interface{}{
					"level": "debug",
				},
				"kcp": map[string]interface{}{
					"enabled": true,
				},
			},
			desiredField: map[string]interface{}{
				"kcp": map[string]interface{}{
					"enabled": true,
				},
			},
			fieldName:    specFieldValues,
			expectMerged: true,
			expectError:  false,
		},
		{
			name:          "only desired exists - should use desired",
			existingField: nil,
			desiredField:  map[string]interface{}{"desired": "value"},
			fieldName:     specFieldValues,
			expectMerged:  true,
			expectError:   false,
		},
		{
			name:          "only existing exists - should keep existing",
			existingField: map[string]interface{}{"existing": "value"},
			desiredField:  nil,
			fieldName:     specFieldValues,
			expectMerged:  false, // No-op, existing kept
			expectError:   false,
		},
		{
			name:          "neither exists - no-op",
			existingField: nil,
			desiredField:  nil,
			fieldName:     specFieldValues,
			expectMerged:  false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			existing := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{},
				},
			}
			desired := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{},
				},
			}

			existingSpec := existing.Object["spec"].(map[string]interface{})
			desiredSpec := desired.Object["spec"].(map[string]interface{})

			if tt.existingField != nil {
				existingSpec[tt.fieldName] = tt.existingField
			}
			if tt.desiredField != nil {
				desiredSpec[tt.fieldName] = tt.desiredField
			}

			err := mergeHelmReleaseField(existing, existingSpec, desiredSpec, tt.fieldName, s.log)
			if tt.expectError {
				s.Error(err)
				return
			}
			s.NoError(err)

			if tt.expectMerged {
				result, found, err := unstructured.NestedMap(existing.Object, "spec", tt.fieldName)
				s.NoError(err)
				s.True(found, "Field should exist after merge")
				s.NotNil(result)

				// Verify deletion behavior: keys not in desired should be removed
				if tt.name == "removes keys not in desired" {
					s.Contains(result, "a")
					s.Contains(result, "b")
					s.NotContains(result, "c", "Key 'c' should be removed as it's not in desired")
				}
				// Verify nested field deletion
				if tt.name == "removes nested keys not in desired" {
					s.Contains(result, "kcp")
					s.NotContains(result, "log", "Key 'log' should be removed as it's not in desired")
					kcp, ok := result["kcp"].(map[string]interface{})
					s.True(ok)
					s.Equal(true, kcp["enabled"])
				}
			}
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_mergeHelmReleaseSpec() {
	tests := []struct {
		name        string
		existing    *unstructured.Unstructured
		desired     *unstructured.Unstructured
		expectError bool
		validate    func(*unstructured.Unstructured)
	}{
		{
			name: "merge values and chart fields",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"values": map[string]interface{}{
							"existing": "value",
							"shared":   "existing",
						},
						"chart": map[string]interface{}{
							"version": "1.0.0",
						},
						"interval": "5m",
					},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"values": map[string]interface{}{
							"desired": "value",
							"shared":  "desired",
						},
						"chart": map[string]interface{}{
							"version": "2.0.0",
						},
						"interval": "10m",
					},
				},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Values should be merged (desired takes precedence for shared keys)
				// Keys not in desired should be removed
				values, found, err := unstructured.NestedMap(obj.Object, "spec", "values")
				s.NoError(err)
				s.True(found)
				s.Equal("desired", values["shared"]) // Desired takes precedence
				s.NotContains(values, "existing", "Key 'existing' should be removed as it's not in desired")
				s.Equal("value", values["desired"])

				// Interval should be updated (desired takes precedence for non-merged fields)
				interval, found, err := unstructured.NestedString(obj.Object, "spec", "interval")
				s.NoError(err)
				s.True(found)
				s.Equal("10m", interval)
			},
		},
		{
			name: "desired has no spec",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"values": map[string]interface{}{"key": "value"},
					},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Existing spec should remain unchanged
				values, found, err := unstructured.NestedMap(obj.Object, "spec", "values")
				s.NoError(err)
				s.True(found)
				s.Equal("value", values["key"])
			},
		},
		{
			name: "existing has no spec",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"values": map[string]interface{}{"key": "value"},
					},
				},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Desired spec should be set
				values, found, err := unstructured.NestedMap(obj.Object, "spec", "values")
				s.NoError(err)
				s.True(found)
				s.Equal("value", values["key"])
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := mergeHelmReleaseSpec(tt.existing, tt.desired, s.log)
			if tt.expectError {
				s.Error(err)
				return
			}
			s.NoError(err)
			if tt.validate != nil {
				tt.validate(tt.existing)
			}
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_mergeGenericSpec() {
	tests := []struct {
		name        string
		existing    *unstructured.Unstructured
		desired     *unstructured.Unstructured
		expectError bool
		validate    func(*unstructured.Unstructured)
	}{
		{
			name: "merge generic spec",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"existing": "value",
						"shared":   "existing",
					},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{
						"desired": "value",
						"shared":  "desired",
					},
				},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Existing should take precedence for shared keys
				spec, found, err := unstructured.NestedMap(obj.Object, "spec")
				s.NoError(err)
				s.True(found)
				s.Equal("existing", spec["shared"])
				s.Equal("value", spec["existing"])
				s.Equal("value", spec["desired"])
			},
		},
		{
			name: "desired has no spec",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"key": "value"},
				},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Existing spec should remain
				spec, found, err := unstructured.NestedMap(obj.Object, "spec")
				s.NoError(err)
				s.True(found)
				s.Equal("value", spec["key"])
			},
		},
		{
			name: "existing has no spec",
			existing: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			desired: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"spec": map[string]interface{}{"key": "value"},
				},
			},
			expectError: false,
			validate: func(obj *unstructured.Unstructured) {
				// Desired spec should be set
				spec, found, err := unstructured.NestedMap(obj.Object, "spec")
				s.NoError(err)
				s.True(found)
				s.Equal("value", spec["key"])
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := mergeGenericSpec(tt.existing, tt.desired, s.log)
			if tt.expectError {
				s.Error(err)
				return
			}
			s.NoError(err)
			if tt.validate != nil {
				tt.validate(tt.existing)
			}
		})
	}
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

	clientMock.EXPECT().Create(ctx, obj, client.FieldOwner(fieldManagerDeployment)).Return(nil)

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
