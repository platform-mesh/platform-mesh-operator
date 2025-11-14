package subroutines_test

import (
	"context"
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"

	pmconfig "github.com/platform-mesh/golang-commons/config"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	ctrl "sigs.k8s.io/controller-runtime"
)

type DeployTestSuite struct {
	suite.Suite
	clientMock *mocks.Client
	helperMock *mocks.KcpHelper
	testObj    *subroutines.DeploymentSubroutine
	log        *logger.Logger
}

func TestDeployTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTestSuite))
}

func (s *DeployTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	cfgLog := logger.DefaultConfig()
	cfgLog.Level = "debug"
	cfgLog.NoJSON = true
	cfgLog.Name = "DeployTestSuite"
	s.log, _ = logger.New(cfgLog)

	cfg := pmconfig.CommonServiceConfig{}
	operatorCfg := config.OperatorConfig{
		WorkspaceDir: "../../",
	}

	s.testObj = subroutines.NewDeploymentSubroutine(s.clientMock, &cfg, &operatorCfg)
}

func (s *DeployTestSuite) Test_applyReleaseWithValues() {
	ctx := context.TODO()

	inst := &v1alpha1.PlatformMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-platform-mesh",
			Namespace: "default",
		},
		Spec: v1alpha1.PlatformMeshSpec{},
	}

	// mocks
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Namespace: "default", Name: "rebac-authz-webhook-cert"}, mock.Anything).Return(nil).Twice()
	s.clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			// Simulate a successful patch operation
			hr := obj.(*unstructured.Unstructured)

			// Extract .spec
			spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
			s.Require().NoError(err, "should be able to get spec")
			s.Require().True(found, "spec should be present")

			// Check if spec is a map
			specMap, ok := spec.(map[string]interface{})
			s.Require().True(ok, "spec should be a map[string]interface{}")

			// Extract .spec.values
			specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
			s.Require().NoError(err, "should be able to get spec.values")
			s.Require().True(found, "spec.values should be present")

			specJSON, ok := specValues.(apiextensionsv1.JSON)
			s.Require().True(ok, "spec.values should be of type apiextensionsv1.JSON")

			expected := `{"baseDomain":"portal.dev.local","baseDomainPort":"portal.dev.local:8443","iamWebhookCA":"","port":"8443","protocol":"https","services":{"services":{"platform-mesh-operator":{"version":"v1.0.0"}}}}`
			s.Require().Equal(expected, string(specJSON.Raw), "spec.values.Raw should match expected JSON string")

			return nil
		},
	).Once()

	// Create DeploymentComponents Version
	templateVars, err := subroutines.TemplateVars(ctx, inst, s.clientMock)
	s.Assert().NoError(err, "TemplateVars should not return an error")

	vals := apiextensionsv1.JSON{Raw: []byte(`{"services": {"platform-mesh-operator": {"version": "v1.0.0"}}}`)}
	instance := &v1alpha1.PlatformMesh{
		Spec: v1alpha1.PlatformMeshSpec{
			Values: vals,
		},
	}

	mergedValues, err := subroutines.MergeValuesAndServices(instance, templateVars)
	s.Assert().NoError(err, "MergeValuesAndServices should not return an error")

	err = s.testObj.ApplyReleaseWithValues(ctx, "../../manifests/k8s/platform-mesh-operator-components/release.yaml", s.clientMock, mergedValues)
	s.Assert().NoError(err, "ApplyReleaseWithValues should not return an error")

	// switch to standard port 443
	inst.Spec.Exposure = &v1alpha1.ExposureConfig{
		Port: 443,
	}

	templateVars, err = subroutines.TemplateVars(ctx, inst, s.clientMock)
	s.Assert().NoError(err, "TemplateVars should not return an error")

	s.clientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			// Simulate a successful patch operation
			hr := obj.(*unstructured.Unstructured)

			// Extract .spec
			spec, found, err := unstructured.NestedFieldNoCopy(hr.Object, "spec")
			s.Require().NoError(err, "should be able to get spec")
			s.Require().True(found, "spec should be present")

			// Check if spec is a map
			specMap, ok := spec.(map[string]interface{})
			s.Require().True(ok, "spec should be a map[string]interface{}")

			// Extract .spec.values
			specValues, found, err := unstructured.NestedFieldNoCopy(specMap, "values")
			s.Require().NoError(err, "should be able to get spec.values")
			s.Require().True(found, "spec.values should be present")

			specJSON, ok := specValues.(apiextensionsv1.JSON)
			s.Require().True(ok, "spec.values should be of type apiextensionsv1.JSON")

			expected := `{"baseDomain":"portal.dev.local","baseDomainPort":"portal.dev.local","iamWebhookCA":"","port":"443","protocol":"https","services":{"services":{"platform-mesh-operator":{"version":"v1.0.0"}}}}`
			s.Require().Equal(expected, string(specJSON.Raw), "spec.values.Raw should match expected JSON string")

			return nil
		},
	).Once()

	mergedValues, err = subroutines.MergeValuesAndServices(instance, templateVars)
	s.Assert().NoError(err, "MergeValuesAndServices should not return an error")

	err = s.testObj.ApplyReleaseWithValues(ctx, "../../manifests/k8s/platform-mesh-operator-components/release.yaml", s.clientMock, mergedValues)
	s.Assert().NoError(err, "ApplyReleaseWithValues should not return an error")

}

func (s *DeployTestSuite) Test_Finalize_DeletesFluxObjects() {
	ctx := context.TODO()

	// mock deletions
	s.clientMock.EXPECT().Delete(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		u := obj.(*unstructured.Unstructured)
		gvk := u.GroupVersionKind()
		return gvk.Group == "delivery.ocm.software" && gvk.Version == "v1alpha1" && gvk.Kind == "Resource" && u.GetName() == "platform-mesh-operator-components" && u.GetNamespace() == "default"
	})).Return(nil).Once()

	s.clientMock.EXPECT().Delete(mock.Anything, mock.MatchedBy(func(obj client.Object) bool {
		u := obj.(*unstructured.Unstructured)
		gvk := u.GroupVersionKind()
		return gvk.Group == "helm.toolkit.fluxcd.io" && gvk.Version == "v2" && gvk.Kind == "HelmRelease" && u.GetName() == "platform-mesh-operator-components" && u.GetNamespace() == "default"
	})).Return(nil).Once()

	// asserts
	res, opErr := s.testObj.Finalize(ctx, &v1alpha1.PlatformMesh{})
	s.Require().Nil(opErr)
	s.Require().Equal(ctrl.Result{}, res)
}

func (s *DeployTestSuite) Test_Finalize_AlreadyDeleted() {
	ctx := context.TODO()

	// mock deletions with NotFound errors
	s.clientMock.EXPECT().Delete(mock.Anything, mock.Anything).Return(kerrors.NewNotFound(schema.GroupResource{Group: "delivery.ocm.software", Resource: "Resource"}, "platform-mesh-operator-components")).Once()
	s.clientMock.EXPECT().Delete(mock.Anything, mock.Anything).Return(kerrors.NewNotFound(schema.GroupResource{Group: "helm.toolkit.fluxcd.io", Resource: "HelmRelease"}, "platform-mesh-operator-components")).Once()

	res, opErr := s.testObj.Finalize(ctx, &v1alpha1.PlatformMesh{})
	s.Require().Nil(opErr)
	s.Require().Equal(ctrl.Result{}, res)
}
