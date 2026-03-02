package subroutines

import (
	"context"
	"testing"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
				s.NotNil(labels, "labels should be preserved")
				s.Equal("label", labels["existing"], "existing label should be preserved")
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

func (s *DeploymentHelpersTestSuite) Test_isZeroValue() {
	tests := []struct {
		name     string
		value    interface{}
		expected bool
	}{
		{
			name:     "nil value",
			value:    nil,
			expected: true,
		},
		{
			name:     "empty string",
			value:    "",
			expected: true,
		},
		{
			name:     "non-empty string",
			value:    "hello",
			expected: false,
		},
		{
			name:     "empty slice",
			value:    []interface{}{},
			expected: true,
		},
		{
			name:     "non-empty slice",
			value:    []interface{}{"a"},
			expected: false,
		},
		{
			name:     "empty map",
			value:    map[string]interface{}{},
			expected: true,
		},
		{
			name:     "non-empty map",
			value:    map[string]interface{}{"key": "value"},
			expected: false,
		},
		{
			name:     "zero int",
			value:    0,
			expected: true,
		},
		{
			name:     "non-zero int",
			value:    42,
			expected: false,
		},
		{
			name:     "false bool",
			value:    false,
			expected: true,
		},
		{
			name:     "true bool",
			value:    true,
			expected: false,
		},
		{
			name:     "zero float",
			value:    0.0,
			expected: true,
		},
		{
			name:     "non-zero float",
			value:    3.14,
			expected: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := isZeroValue(tt.value)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_default() {
	funcMap := templateFuncMap()
	defaultFunc := funcMap["default"].(func(interface{}, interface{}) interface{})

	tests := []struct {
		name         string
		defaultValue interface{}
		actualValue  interface{}
		expected     interface{}
	}{
		{
			name:         "use default when value is nil",
			defaultValue: "default",
			actualValue:  nil,
			expected:     "default",
		},
		{
			name:         "use default when value is empty string",
			defaultValue: "default",
			actualValue:  "",
			expected:     "default",
		},
		{
			name:         "use actual value when non-empty",
			defaultValue: "default",
			actualValue:  "actual",
			expected:     "actual",
		},
		{
			name:         "use default when value is empty slice",
			defaultValue: []string{"default"},
			actualValue:  []interface{}{},
			expected:     []string{"default"},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := defaultFunc(tt.defaultValue, tt.actualValue)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_toYaml() {
	funcMap := templateFuncMap()
	toYamlFunc := funcMap["toYaml"].(func(interface{}) (string, error))

	tests := []struct {
		name        string
		value       interface{}
		expected    string
		expectError bool
	}{
		{
			name:     "simple map",
			value:    map[string]interface{}{"key": "value"},
			expected: "key: value\n",
		},
		{
			name:     "nested map",
			value:    map[string]interface{}{"outer": map[string]interface{}{"inner": "value"}},
			expected: "outer:\n  inner: value\n",
		},
		{
			name:     "slice",
			value:    []string{"a", "b", "c"},
			expected: "- a\n- b\n- c\n",
		},
		{
			name:     "string",
			value:    "simple",
			expected: "simple\n",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := toYamlFunc(tt.value)
			if tt.expectError {
				s.Error(err)
			} else {
				s.NoError(err)
				s.Equal(tt.expected, result)
			}
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_nindent() {
	funcMap := templateFuncMap()
	nindentFunc := funcMap["nindent"].(func(int, string) string)

	tests := []struct {
		name     string
		spaces   int
		input    string
		expected string
	}{
		{
			name:     "empty string",
			spaces:   4,
			input:    "",
			expected: "",
		},
		{
			name:     "single line",
			spaces:   2,
			input:    "hello",
			expected: "  hello\n",
		},
		{
			name:     "multiple lines",
			spaces:   4,
			input:    "line1\nline2\nline3",
			expected: "    line1\n    line2\n    line3\n",
		},
		{
			name:     "lines with trailing newline",
			spaces:   2,
			input:    "line1\nline2\n",
			expected: "  line1\n  line2\n",
		},
		{
			name:     "lines with empty lines at start",
			spaces:   2,
			input:    "\n\nline1",
			expected: "  line1\n",
		},
		{
			name:     "zero spaces",
			spaces:   0,
			input:    "hello",
			expected: "hello\n",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := nindentFunc(tt.spaces, tt.input)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_or() {
	funcMap := templateFuncMap()
	orFunc := funcMap["or"].(func(interface{}, interface{}) interface{})

	tests := []struct {
		name     string
		a        interface{}
		b        interface{}
		expected interface{}
	}{
		{
			name:     "first non-zero",
			a:        "first",
			b:        "second",
			expected: "first",
		},
		{
			name:     "first zero, second non-zero",
			a:        "",
			b:        "second",
			expected: "second",
		},
		{
			name:     "both zero",
			a:        "",
			b:        "",
			expected: "",
		},
		{
			name:     "first nil",
			a:        nil,
			b:        "second",
			expected: "second",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := orFunc(tt.a, tt.b)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_and() {
	funcMap := templateFuncMap()
	andFunc := funcMap["and"].(func(interface{}, interface{}) bool)

	tests := []struct {
		name     string
		a        interface{}
		b        interface{}
		expected bool
	}{
		{
			name:     "both non-zero",
			a:        "first",
			b:        "second",
			expected: true,
		},
		{
			name:     "first zero",
			a:        "",
			b:        "second",
			expected: false,
		},
		{
			name:     "second zero",
			a:        "first",
			b:        "",
			expected: false,
		},
		{
			name:     "both zero",
			a:        "",
			b:        "",
			expected: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := andFunc(tt.a, tt.b)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_templateFuncMap_not() {
	funcMap := templateFuncMap()
	notFunc := funcMap["not"].(func(interface{}) bool)

	tests := []struct {
		name     string
		value    interface{}
		expected bool
	}{
		{
			name:     "non-zero string",
			value:    "hello",
			expected: false,
		},
		{
			name:     "empty string",
			value:    "",
			expected: true,
		},
		{
			name:     "nil",
			value:    nil,
			expected: true,
		},
		{
			name:     "true bool",
			value:    true,
			expected: false,
		},
		{
			name:     "false bool",
			value:    false,
			expected: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := notFunc(tt.value)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_preserveExistingArgoSourceFields() {
	tests := []struct {
		name                 string
		existingApp          *unstructured.Unstructured
		objMap               map[string]interface{}
		expectedRepoURL      string
		expectedRevPreserved bool
	}{
		{
			name:        "app does not exist - nothing to preserve",
			existingApp: nil,
			objMap: map[string]interface{}{
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"repoURL":        "https://new-repo.git",
						"targetRevision": "v1.0.0",
					},
				},
			},
			expectedRepoURL: "https://new-repo.git",
		},
		{
			name: "existing app has placeholder values - should not preserve",
			existingApp: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "test-app",
						"namespace": "argocd",
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"repoURL":        argoPlaceholderRepoURL,
							"targetRevision": argoPlaceholderRepoURL,
						},
					},
				},
			},
			objMap: map[string]interface{}{
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"repoURL":        "https://new-repo.git",
						"targetRevision": "v1.0.0",
					},
				},
			},
			expectedRepoURL: "https://new-repo.git",
		},
		{
			name: "existing app has real values different from new - should preserve",
			existingApp: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "test-app",
						"namespace": "argocd",
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"repoURL":        "https://existing-repo.git",
							"targetRevision": "v0.9.0",
						},
					},
				},
			},
			objMap: map[string]interface{}{
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"repoURL":        "https://new-repo.git",
						"targetRevision": "v1.0.0",
					},
				},
			},
			expectedRepoURL:      "",
			expectedRevPreserved: true,
		},
		{
			name: "existing app has same values as new - no preservation needed",
			existingApp: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "test-app",
						"namespace": "argocd",
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"repoURL":        "https://same-repo.git",
							"targetRevision": "v1.0.0",
						},
					},
				},
			},
			objMap: map[string]interface{}{
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"repoURL":        "https://same-repo.git",
						"targetRevision": "v1.0.0",
					},
				},
			},
			expectedRepoURL: "https://same-repo.git",
		},
		{
			name: "existing app has empty repoURL - should not preserve",
			existingApp: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]interface{}{
						"name":      "test-app",
						"namespace": "argocd",
					},
					"spec": map[string]interface{}{
						"source": map[string]interface{}{
							"repoURL":        "",
							"targetRevision": "",
						},
					},
				},
			},
			objMap: map[string]interface{}{
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"repoURL":        "https://new-repo.git",
						"targetRevision": "v1.0.0",
					},
				},
			},
			expectedRepoURL: "https://new-repo.git",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			ctx := context.Background()

			var fakeClient client.Client
			if tt.existingApp != nil {
				fakeClient = fake.NewClientBuilder().
					WithObjects(tt.existingApp).
					Build()
			} else {
				fakeClient = fake.NewClientBuilder().Build()
			}

			cfg := pmconfig.CommonServiceConfig{}
			operatorCfg := config.OperatorConfig{
				WorkspaceDir: "../../",
			}

			sub := &DeploymentSubroutine{
				clientInfra: fakeClient,
				cfg:         &cfg,
				cfgOperator: &operatorCfg,
			}

			sub.preserveExistingArgoSourceFields(ctx, tt.objMap, "test-app", "argocd", s.log)

			spec := tt.objMap["spec"].(map[string]interface{})
			source := spec["source"].(map[string]interface{})

			if tt.expectedRevPreserved {
				_, hasRepoURL := source["repoURL"]
				_, hasTargetRevision := source["targetRevision"]
				s.False(hasRepoURL, "repoURL should have been deleted to preserve existing")
				s.False(hasTargetRevision, "targetRevision should have been deleted to preserve existing")
			} else if tt.expectedRepoURL != "" {
				s.Equal(tt.expectedRepoURL, source["repoURL"])
			}
		})
	}
}

func (s *DeploymentHelpersTestSuite) Test_preserveExistingArgoSourceFields_GetError() {
	ctx := context.Background()

	fakeClient := fake.NewClientBuilder().Build()

	cfg := pmconfig.CommonServiceConfig{}
	operatorCfg := config.OperatorConfig{
		WorkspaceDir: "../../",
	}

	sub := &DeploymentSubroutine{
		clientInfra: fakeClient,
		cfg:         &cfg,
		cfgOperator: &operatorCfg,
	}

	objMap := map[string]interface{}{
		"spec": map[string]interface{}{
			"source": map[string]interface{}{
				"repoURL":        "https://new-repo.git",
				"targetRevision": "v1.0.0",
			},
		},
	}

	sub.preserveExistingArgoSourceFields(ctx, objMap, "nonexistent-app", "argocd", s.log)

	spec := objMap["spec"].(map[string]interface{})
	source := spec["source"].(map[string]interface{})
	s.Equal("https://new-repo.git", source["repoURL"])
	s.Equal("v1.0.0", source["targetRevision"])
}
