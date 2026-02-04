package subroutines

import (
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
