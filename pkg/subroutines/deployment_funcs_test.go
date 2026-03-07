package subroutines

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DeploymentFuncsTestSuite struct {
	suite.Suite
}

func TestDeploymentFuncsTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentFuncsTestSuite))
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_StringWithTemplate() {
	templateData := map[string]interface{}{
		"namespace": "prod",
		"config": map[string]interface{}{
			"clusterName": "cluster-1",
			"syncWave":    "10",
		},
	}

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "simple template",
			input:    "ns-{{ .namespace }}",
			expected: "ns-prod",
		},
		{
			name:     "nested map access",
			input:    "wave-{{ .config.syncWave }}",
			expected: "wave-10",
		},
		{
			name:     "multiple templates in string",
			input:    "{{ .namespace }}-{{ .config.clusterName }}",
			expected: "prod-cluster-1",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := renderTemplatesInValue(tt.input, templateData)
			s.NoError(err)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_StringWithoutTemplate() {
	templateData := map[string]interface{}{
		"namespace": "prod",
	}

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "plain string",
			input:    "plain-text",
			expected: "plain-text",
		},
		{
			name:     "string with single brace",
			input:    "value with { single brace",
			expected: "value with { single brace",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := renderTemplatesInValue(tt.input, templateData)
			s.NoError(err)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_MapWithNestedTemplates() {
	templateData := map[string]interface{}{
		"namespace": "prod",
		"config": map[string]interface{}{
			"clusterName": "cluster-1",
		},
	}

	input := map[string]interface{}{
		"name":  "{{ .config.clusterName }}",
		"port":  8080,
		"label": "static-value",
		"nested": map[string]interface{}{
			"ns": "{{ .namespace }}",
		},
	}

	result, err := renderTemplatesInValue(input, templateData)
	s.NoError(err)

	resultMap := result.(map[string]interface{})
	s.Equal("cluster-1", resultMap["name"])
	s.Equal(8080, resultMap["port"])
	s.Equal("static-value", resultMap["label"])

	nestedMap := resultMap["nested"].(map[string]interface{})
	s.Equal("prod", nestedMap["ns"])
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_SliceWithTemplates() {
	templateData := map[string]interface{}{
		"namespace": "prod",
		"env":       "production",
	}

	input := []interface{}{"{{ .namespace }}", "static", "{{ .env }}"}

	result, err := renderTemplatesInValue(input, templateData)
	s.NoError(err)

	resultSlice := result.([]interface{})
	s.Equal("prod", resultSlice[0])
	s.Equal("static", resultSlice[1])
	s.Equal("production", resultSlice[2])
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_NonStringValues() {
	templateData := map[string]interface{}{
		"namespace": "prod",
	}

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "integer",
			input:    42,
			expected: 42,
		},
		{
			name:     "float",
			input:    3.14,
			expected: 3.14,
		},
		{
			name:     "bool true",
			input:    true,
			expected: true,
		},
		{
			name:     "bool false",
			input:    false,
			expected: false,
		},
		{
			name:     "nil",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := renderTemplatesInValue(tt.input, templateData)
			s.NoError(err)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_InvalidTemplate() {
	templateData := map[string]interface{}{
		"namespace": "prod",
	}

	// Invalid template syntax - should return original value without error
	input := "{{ .namespace"
	result, err := renderTemplatesInValue(input, templateData)
	s.NoError(err)
	s.Equal(input, result)
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_MissingVariable() {
	templateData := map[string]interface{}{
		"namespace": "prod",
	}

	// Missing variable - template execution should handle gracefully
	input := "value-{{ .missing }}"
	result, err := renderTemplatesInValue(input, templateData)
	s.NoError(err)
	// Should render with empty value for missing key
	s.Equal("value-<no value>", result)
}

func (s *DeploymentFuncsTestSuite) Test_renderTemplatesInValue_WithTemplateFunctions() {
	templateData := map[string]interface{}{
		"value":   "",
		"present": "exists",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "default function with empty value",
			input:    "{{ default \"fallback\" .value }}",
			expected: "fallback",
		},
		{
			name:     "default function with present value",
			input:    "{{ default \"fallback\" .present }}",
			expected: "exists",
		},
		{
			name:     "or function",
			input:    "{{ or .value .present }}",
			expected: "exists",
		},
		{
			name:     "not function with empty",
			input:    "{{ if not .value }}empty{{ else }}has-value{{ end }}",
			expected: "empty",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result, err := renderTemplatesInValue(tt.input, templateData)
			s.NoError(err)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_NilServices() {
	err := calculateSyncWaves(nil, "default")
	s.NoError(err)
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_EmptyServices() {
	services := map[string]interface{}{}
	err := calculateSyncWaves(services, "default")
	s.NoError(err)
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_NoDependencies() {
	services := map[string]interface{}{
		"serviceA": map[string]interface{}{
			"enabled": true,
		},
		"serviceB": map[string]interface{}{
			"enabled": true,
		},
		"serviceC": map[string]interface{}{
			"enabled": true,
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// All services should be at wave 0
	for name, svc := range services {
		svcMap := svc.(map[string]interface{})
		s.Equal(0, svcMap["syncWave"], "service %s should be wave 0", name)
	}
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_WithDependencies() {
	services := map[string]interface{}{
		"database": map[string]interface{}{
			"enabled": true,
		},
		"cache": map[string]interface{}{
			"enabled": true,
		},
		"api": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "database"},
				map[string]interface{}{"name": "cache"},
			},
		},
		"frontend": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "api"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// database and cache should be wave 0
	s.Equal(0, services["database"].(map[string]interface{})["syncWave"])
	s.Equal(0, services["cache"].(map[string]interface{})["syncWave"])
	// api depends on database and cache, should be wave 1
	s.Equal(1, services["api"].(map[string]interface{})["syncWave"])
	// frontend depends on api, should be wave 2
	s.Equal(2, services["frontend"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_UserConfiguredPreserved() {
	services := map[string]interface{}{
		"database": map[string]interface{}{
			"enabled": true,
		},
		"api": map[string]interface{}{
			"enabled":  true,
			"syncWave": 5, // user-configured
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "database"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// database should be wave 0
	s.Equal(0, services["database"].(map[string]interface{})["syncWave"])
	// api should preserve user-configured wave 5 (not calculated wave 1)
	s.Equal(5, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_UserConfiguredAsFloat64() {
	// JSON unmarshaling produces float64 for numbers
	services := map[string]interface{}{
		"service": map[string]interface{}{
			"enabled":  true,
			"syncWave": float64(3),
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// User-configured value should be preserved (skipped in the final update loop)
	// The syncWave remains as float64(3) since it was user-configured
	s.Equal(float64(3), services["service"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_UserConfiguredAsInt64() {
	services := map[string]interface{}{
		"service": map[string]interface{}{
			"enabled":  true,
			"syncWave": int64(7),
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// User-configured value should be preserved (skipped in the final update loop)
	// The syncWave remains as int64(7) since it was user-configured
	s.Equal(int64(7), services["service"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_DependencyNotInServices() {
	services := map[string]interface{}{
		"api": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "nonexistent-service"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// api should be wave 0 since dependency doesn't exist
	s.Equal(0, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_InvalidDependsOnFormat() {
	services := map[string]interface{}{
		"api": map[string]interface{}{
			"enabled":   true,
			"dependsOn": "invalid-string-format", // should be a slice
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// Should handle gracefully and set wave 0
	s.Equal(0, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_DependsOnWithInvalidItems() {
	services := map[string]interface{}{
		"database": map[string]interface{}{
			"enabled": true,
		},
		"api": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				"invalid-string-item", // should be map
				map[string]interface{}{"name": "database"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// api should still get wave 1 from valid database dependency
	s.Equal(1, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_DependsOnMissingName() {
	services := map[string]interface{}{
		"api": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"namespace": "other"}, // missing "name" key
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// api should be wave 0 since no valid dependency name
	s.Equal(0, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_ServiceConfigNotMap() {
	services := map[string]interface{}{
		"stringService": "not-a-map",
		"api": map[string]interface{}{
			"enabled": true,
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	// api should still work
	s.Equal(0, services["api"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_ChainedDependencies() {
	// A -> B -> C -> D (chain of 4)
	services := map[string]interface{}{
		"serviceD": map[string]interface{}{
			"enabled": true,
		},
		"serviceC": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceD"},
			},
		},
		"serviceB": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceC"},
			},
		},
		"serviceA": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceB"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	s.Equal(0, services["serviceD"].(map[string]interface{})["syncWave"])
	s.Equal(1, services["serviceC"].(map[string]interface{})["syncWave"])
	s.Equal(2, services["serviceB"].(map[string]interface{})["syncWave"])
	s.Equal(3, services["serviceA"].(map[string]interface{})["syncWave"])
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_DiamondDependency() {
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	services := map[string]interface{}{
		"serviceD": map[string]interface{}{
			"enabled": true,
		},
		"serviceB": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceD"},
			},
		},
		"serviceC": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceD"},
			},
		},
		"serviceA": map[string]interface{}{
			"enabled": true,
			"dependsOn": []interface{}{
				map[string]interface{}{"name": "serviceB"},
				map[string]interface{}{"name": "serviceC"},
			},
		},
	}

	err := calculateSyncWaves(services, "default")
	s.NoError(err)

	s.Equal(0, services["serviceD"].(map[string]interface{})["syncWave"])
	s.Equal(1, services["serviceB"].(map[string]interface{})["syncWave"])
	s.Equal(1, services["serviceC"].(map[string]interface{})["syncWave"])
	s.Equal(2, services["serviceA"].(map[string]interface{})["syncWave"])
}
