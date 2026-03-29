package subroutines

import (
	"context"
	"encoding/json"
	"testing"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
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
	err := calculateSyncWaves(nil)
	s.NoError(err)
}

func (s *DeploymentFuncsTestSuite) Test_calculateSyncWaves_EmptyServices() {
	services := map[string]interface{}{}
	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
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

	err := calculateSyncWaves(services)
	s.NoError(err)

	s.Equal(0, services["serviceD"].(map[string]interface{})["syncWave"])
	s.Equal(1, services["serviceB"].(map[string]interface{})["syncWave"])
	s.Equal(1, services["serviceC"].(map[string]interface{})["syncWave"])
	s.Equal(2, services["serviceA"].(map[string]interface{})["syncWave"])
}

// ---- buildRuntimeTemplateVars and buildComponentsTemplateVars tests ----

type TemplateVarsTestSuite struct {
	suite.Suite
	scheme *runtime.Scheme
}

func TestTemplateVarsTestSuite(t *testing.T) {
	suite.Run(t, new(TemplateVarsTestSuite))
}

func (s *TemplateVarsTestSuite) SetupSuite() {
	s.scheme = runtime.NewScheme()
	s.Require().NoError(clientgoscheme.AddToScheme(s.scheme))
	s.Require().NoError(v1alpha1.AddToScheme(s.scheme))
}

// newSubroutineWithProfile creates a DeploymentSubroutine backed by a fake
// clientRuntime that already contains a profile ConfigMap for the given inst.
func (s *TemplateVarsTestSuite) newSubroutineWithProfile(profileYAML string, remoteRuntime config.RemoteClusterConfig) (*DeploymentSubroutine, *v1alpha1.PlatformMesh) {
	inst := &v1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pm",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.PlatformMeshSpec{},
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      inst.Name + defaultProfileConfigMapSuffix,
			Namespace: inst.Namespace,
		},
		Data: map[string]string{
			profileConfigMapKey: profileYAML,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s.scheme).
		WithObjects(inst, cm).
		Build()

	operatorCfg := &config.OperatorConfig{
		RemoteRuntime: remoteRuntime,
	}
	cfg := &pmconfig.CommonServiceConfig{}

	sub := &DeploymentSubroutine{
		clientRuntime: fakeClient,
		clientInfra:   fakeClient,
		cfg:           cfg,
		cfgOperator:   operatorCfg,
	}
	return sub, inst
}

// minimalProfileYAML is a valid profile with empty infra and components sections.
const minimalProfileYAML = `
infra:
  baseDomain: example.com
components:
  services: {}
`

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_BasicMerge() {
	profileYAML := `
infra:
  baseDomain: profile.example.com
  infraKey: fromProfile
components:
  services: {}
`
	sub, inst := s.newSubroutineWithProfile(profileYAML, config.RemoteClusterConfig{})

	templateVars := apiextensionsv1.JSON{Raw: []byte(`{"templateKey":"fromTemplateVars"}`)}
	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, templateVars)

	s.Require().NoError(err)
	s.Equal("profile.example.com", result["baseDomain"])
	s.Equal("fromProfile", result["infraKey"])
	s.Equal("fromTemplateVars", result["templateKey"])
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_TemplateVarsOverrideProfile() {
	profileYAML := `
infra:
  baseDomain: profile.example.com
  sharedKey: fromProfile
components:
  services: {}
`
	sub, inst := s.newSubroutineWithProfile(profileYAML, config.RemoteClusterConfig{})

	templateVars := apiextensionsv1.JSON{Raw: []byte(`{"sharedKey":"fromTemplateVars"}`)}
	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, templateVars)

	s.Require().NoError(err)
	// templateVars should win over profile
	s.Equal("fromTemplateVars", result["sharedKey"])
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_SpecValuesOverride() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	specValues := map[string]interface{}{"specKey": "fromSpec"}
	raw, err := json.Marshal(specValues)
	s.Require().NoError(err)
	inst.Spec.Values = apiextensionsv1.JSON{Raw: raw}

	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("fromSpec", result["specKey"])
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_OCMConfigMerged() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	inst.Spec.OCM = &v1alpha1.OCMConfig{
		Repo:      &v1alpha1.RepoConfig{Name: "my-repo"},
		Component: &v1alpha1.ComponentConfig{Name: "my-component"},
		ReferencePath: []v1alpha1.ReferencePathElement{
			{Name: "path-element"},
		},
	}

	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	ocm, ok := result["ocm"].(map[string]interface{})
	s.Require().True(ok, "expected ocm key in result")
	repo, ok := ocm["repo"].(map[string]interface{})
	s.Require().True(ok, "expected repo in ocm")
	s.Equal("my-repo", repo["name"])
	component, ok := ocm["component"].(map[string]interface{})
	s.Require().True(ok, "expected component in ocm")
	s.Equal("my-component", component["name"])
	refs, ok := ocm["referencePath"].([]interface{})
	s.Require().True(ok, "expected referencePath in ocm")
	s.Len(refs, 1)
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_KubeConfigDisabled() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal(false, result["kubeConfigEnabled"])
	_, hasSecretName := result["kubeConfigSecretName"]
	s.False(hasSecretName, "kubeConfigSecretName should not be set when remote runtime disabled")
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_KubeConfigEnabled() {
	remoteRuntime := config.RemoteClusterConfig{
		Kubeconfig:      "/path/to/kubeconfig",
		InfraSecretName: "infra-secret",
		InfraSecretKey:  "kubeconfig",
	}
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, remoteRuntime)

	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal(true, result["kubeConfigEnabled"])
	s.Equal("infra-secret", result["kubeConfigSecretName"])
	s.Equal("kubeconfig", result["kubeConfigSecretKey"])
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_ReleaseNamespace() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	result, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("test-ns", result["releaseNamespace"])
	s.Equal("test-ns", result["helmReleaseNamespace"])
}

func (s *TemplateVarsTestSuite) Test_buildRuntimeTemplateVars_MissingConfigMap() {
	// Subroutine with no ConfigMap in the fake store
	fakeClient := fake.NewClientBuilder().WithScheme(s.scheme).Build()
	operatorCfg := &config.OperatorConfig{}
	cfg := &pmconfig.CommonServiceConfig{}
	sub := &DeploymentSubroutine{
		clientRuntime: fakeClient,
		cfg:           cfg,
		cfgOperator:   operatorCfg,
	}
	inst := &v1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pm", Namespace: "test-ns"},
	}

	_, err := sub.buildRuntimeTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})
	s.Error(err, "expected error when profile ConfigMap is missing")
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_BasicProfile() {
	profileYAML := `
infra: {}
components:
  services:
    myservice:
      enabled: true
      namespace: default
`
	sub, inst := s.newSubroutineWithProfile(profileYAML, config.RemoteClusterConfig{})

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	values, ok := result["values"].(map[string]interface{})
	s.Require().True(ok, "expected values key")
	services, ok := values["services"].(map[string]interface{})
	s.Require().True(ok, "expected services key")
	myService, ok := services["myservice"].(map[string]interface{})
	s.Require().True(ok, "expected myservice in services")
	s.Equal(true, myService["enabled"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_ReleaseNamespace() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("test-ns", result["releaseNamespace"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_SpecValuesServices() {
	profileYAML := `
infra: {}
components:
  services:
    base:
      enabled: true
`
	sub, inst := s.newSubroutineWithProfile(profileYAML, config.RemoteClusterConfig{})

	specValues := map[string]interface{}{
		"services": map[string]interface{}{
			"override": map[string]interface{}{"enabled": true},
		},
	}
	raw, err := json.Marshal(specValues)
	s.Require().NoError(err)
	inst.Spec.Values = apiextensionsv1.JSON{Raw: raw}

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	values, ok := result["values"].(map[string]interface{})
	s.Require().True(ok)
	services, ok := values["services"].(map[string]interface{})
	s.Require().True(ok)
	// Both base (from profile) and override (from spec.Values) should be present
	s.Contains(services, "base")
	s.Contains(services, "override")
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_DeploymentTechnologyDefault() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("fluxcd", result["deploymentTechnology"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_DeploymentTechnologyFromTemplateVars() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	templateVars := apiextensionsv1.JSON{Raw: []byte(`{"deploymentTechnology":"argocd"}`)}
	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, templateVars)

	s.Require().NoError(err)
	s.Equal("argocd", result["deploymentTechnology"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_DeploymentTechnologyInvalidDefaultsToFluxcd() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})

	templateVars := apiextensionsv1.JSON{Raw: []byte(`{"deploymentTechnology":"helm"}`)}
	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, templateVars)

	s.Require().NoError(err)
	s.Equal("fluxcd", result["deploymentTechnology"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_KubeConfigEnabled() {
	remoteRuntime := config.RemoteClusterConfig{
		Kubeconfig:      "/path/to/kubeconfig",
		InfraSecretName: "infra-secret",
		InfraSecretKey:  "kubeconfig",
	}
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, remoteRuntime)

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal(true, result["kubeConfigEnabled"])
	s.Equal("infra-secret", result["kubeConfigSecretName"])
	s.Equal("kubeconfig", result["kubeConfigSecretKey"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_BaseDomainFields() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})
	inst.Spec.Exposure = &v1alpha1.ExposureConfig{
		BaseDomain: "my.domain.com",
		Port:       8443,
	}

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("my.domain.com", result["baseDomain"])
	s.Equal("8443", result["port"])
	s.Equal("my.domain.com:8443", result["baseDomainWithPort"])
}

func (s *TemplateVarsTestSuite) Test_buildComponentsTemplateVars_BaseDomainWithDefaultPort() {
	sub, inst := s.newSubroutineWithProfile(minimalProfileYAML, config.RemoteClusterConfig{})
	inst.Spec.Exposure = &v1alpha1.ExposureConfig{
		BaseDomain: "my.domain.com",
	}

	result, err := sub.buildComponentsTemplateVars(context.Background(), inst, apiextensionsv1.JSON{})

	s.Require().NoError(err)
	s.Equal("443", result["port"])
	// When port is 443, baseDomainWithPort should equal baseDomain
	s.Equal("my.domain.com", result["baseDomainWithPort"])
}
