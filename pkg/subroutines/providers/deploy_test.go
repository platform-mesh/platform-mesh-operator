/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

type DeployTestSuite struct {
	suite.Suite
	testObj    *DeploySubroutine
	clientMock *mocks.Client
	log        *logger.Logger
}

func TestDeployTestSuite(t *testing.T) {
	suite.Run(t, new(DeployTestSuite))
}

func (s *DeployTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "DeployTestSuite"
	s.log, _ = logger.New(cfg)

	s.clientMock = new(mocks.Client)
	s.clientMock.EXPECT().Scheme().Return(runtime.NewScheme()).Maybe()

	var err error
	s.testObj, err = NewDeploySubroutine(s.clientMock)
	s.Require().NoError(err)
}

func (s *DeployTestSuite) TearDownTest() {
	s.clientMock = nil
	s.testObj = nil
}

func (s *DeployTestSuite) newCtx() context.Context {
	return context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
}

// newManagedProvider returns a ManagedProvider with a single RuntimeDeployment
// (github.com/platform-mesh/wildwest-controller → resource name "wildwest-controller").
func (s *DeployTestSuite) newManagedProvider() *providersv1alpha1.ManagedProvider {
	return &providersv1alpha1.ManagedProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cowboys",
			Namespace: "providers-wildwest-ns",
		},
		Spec: providersv1alpha1.ManagedProviderSpec{
			RuntimeDeployments: []providersv1alpha1.ProviderComponentSpec{
				{OCM: &providersv1alpha1.OCMComponentSpec{
					ComponentName: "github.com/platform-mesh/wildwest-controller",
					Version:       "0.1.0",
					Registry:      "ghcr.io/platform-mesh/ocm",
				}},
			},
		},
	}
}

// newManagedProviderWithPortal returns a ManagedProvider with two RuntimeDeployments.
func (s *DeployTestSuite) newManagedProviderWithPortal() *providersv1alpha1.ManagedProvider {
	inst := s.newManagedProvider()
	inst.Spec.RuntimeDeployments = append(inst.Spec.RuntimeDeployments, providersv1alpha1.ProviderComponentSpec{
		OCM: &providersv1alpha1.OCMComponentSpec{
			ComponentName: "github.com/platform-mesh/wildwest-portal",
			Version:       "0.1.0",
			Registry:      "ghcr.io/platform-mesh/ocm",
		},
	})
	return inst
}

func (s *DeployTestSuite) mockCreateOrUpdate(name, namespace string) {
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: name, Namespace: namespace}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, name)).
		Once()
	s.clientMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()
}

func (s *DeployTestSuite) mockHelmReleaseReadyCheck(name, namespace string, ready bool) {
	var conditions []interface{}
	if ready {
		conditions = []interface{}{
			map[string]interface{}{"type": "Ready", "status": "True"},
		}
	}
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: name, Namespace: namespace}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.Object = map[string]interface{}{
				"status": map[string]interface{}{"conditions": conditions},
			}
			return nil
		}).
		Once()
}

// mockExistingNoopOCIRepo simulates an OCIRepository that already exists with a
// matching spec (OperationResultNone) and whose generation matches observedGeneration,
// indicating the artifact controller has fully processed the current spec.
func (s *DeployTestSuite) mockExistingNoopOCIRepo(name, namespace string, ocm *providersv1alpha1.OCMComponentSpec) {
	ociURL := fmt.Sprintf("oci://%s/%s", ocm.Registry, ocm.ComponentName)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: name, Namespace: namespace}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.SetResourceVersion("1")
			u.SetGeneration(1)
			_ = unstructured.SetNestedField(u.Object, ociURL, "spec", "url")
			_ = unstructured.SetNestedField(u.Object, ocm.Version, "spec", "ref", "tag")
			_ = unstructured.SetNestedField(u.Object, "generic", "spec", "provider")
			_ = unstructured.SetNestedField(u.Object, "1m0s", "spec", "interval")
			_ = unstructured.SetNestedField(u.Object, ocm.Insecure, "spec", "insecure")
			_ = unstructured.SetNestedMap(u.Object, map[string]interface{}{
				"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				"operation": "copy",
			}, "spec", "layerSelector")
			_ = unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration")
			return nil
		}).Once()
}

// mockExistingNoopHelmRelease simulates a HelmRelease that already exists with a
// matching spec (OperationResultNone).
func (s *DeployTestSuite) mockExistingNoopHelmRelease(name, namespace string) {
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: name, Namespace: namespace}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.SetResourceVersion("1")
			_ = unstructured.SetNestedField(u.Object, "5m", "spec", "interval")
			_ = unstructured.SetNestedField(u.Object, "OCIRepository", "spec", "chartRef", "kind")
			_ = unstructured.SetNestedField(u.Object, name, "spec", "chartRef", "name")
			_ = unstructured.SetNestedField(u.Object, namespace, "spec", "chartRef", "namespace")
			return nil
		}).Once()
}

// mockComponentDeployed sets up mocks for a component already present on the cluster
// with a matching spec (OperationResultNone, generation reconciled) and the given
// HelmRelease readiness state. Use this to test the condition-check path.
func (s *DeployTestSuite) mockComponentDeployed(name, namespace string, ocm *providersv1alpha1.OCMComponentSpec, ready bool) {
	s.mockExistingNoopOCIRepo(name, namespace, ocm)
	s.mockExistingNoopHelmRelease(name, namespace)
	s.mockHelmReleaseReadyCheck(name, namespace, ready)
}

// --- Process tests ---

func (s *DeployTestSuite) TestProcess_OCIRepositoryCreateFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// Component name "github.com/platform-mesh/wildwest-controller" → "wildwest-controller"
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "wildwest-controller")).
		Once()
	s.clientMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(errors.New("server error")).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to reconcile OCIRepository")
}

func (s *DeployTestSuite) TestProcess_HelmReleaseCreateFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.mockCreateOrUpdate("wildwest-controller", "providers-wildwest-ns") // OCIRepository OK
	// HelmRelease Get → NotFound, Create → error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "wildwest-controller")).
		Once()
	s.clientMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(errors.New("helm release create failed")).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to reconcile HelmRelease")
}

func (s *DeployTestSuite) TestProcess_HelmReleaseGetFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	ocm := inst.Spec.RuntimeDeployments[0].OCM

	s.mockExistingNoopOCIRepo("wildwest-controller", "providers-wildwest-ns", ocm)
	s.mockExistingNoopHelmRelease("wildwest-controller", "providers-wildwest-ns")
	// helmReleaseReady Get → non-404 error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(errors.New("internal server error")).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to get HelmRelease")
}

func (s *DeployTestSuite) TestProcess_ControllerNotReady() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	ocm := inst.Spec.RuntimeDeployments[0].OCM

	s.mockComponentDeployed("wildwest-controller", "providers-wildwest-ns", ocm, false)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_NoPortal() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	ocm := inst.Spec.RuntimeDeployments[0].OCM

	s.mockComponentDeployed("wildwest-controller", "providers-wildwest-ns", ocm, true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseReady, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_PortalNotReady() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()
	controllerOCM := inst.Spec.RuntimeDeployments[0].OCM
	portalOCM := inst.Spec.RuntimeDeployments[1].OCM

	s.mockComponentDeployed("wildwest-controller", "providers-wildwest-ns", controllerOCM, true)
	s.mockComponentDeployed("wildwest-portal", "providers-wildwest-ns", portalOCM, false)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_PortalReady() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()
	controllerOCM := inst.Spec.RuntimeDeployments[0].OCM
	portalOCM := inst.Spec.RuntimeDeployments[1].OCM

	s.mockComponentDeployed("wildwest-controller", "providers-wildwest-ns", controllerOCM, true)
	s.mockComponentDeployed("wildwest-portal", "providers-wildwest-ns", portalOCM, true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseReady, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_HelmReleaseNotFoundDuringReadyCheck() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	ocm := inst.Spec.RuntimeDeployments[0].OCM

	s.mockExistingNoopOCIRepo("wildwest-controller", "providers-wildwest-ns", ocm)
	s.mockExistingNoopHelmRelease("wildwest-controller", "providers-wildwest-ns")
	// helmReleaseReady: NotFound → treated as not ready, no error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "wildwest-controller")).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_WithHelmValues() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	inst.Spec.RuntimeDeployments[0].OCM.Values = apiextensionsv1.JSON{
		Raw: []byte(`{"replicaCount":2,"image":{"tag":"v0.1.0"}}`),
	}

	// Capture the HelmRelease Create call to verify values are injected.
	var capturedHR *unstructured.Unstructured
	s.mockCreateOrUpdate("wildwest-controller", "providers-wildwest-ns") // OCIRepository
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "wildwest-controller")).
		Once()
	s.clientMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			capturedHR = obj.(*unstructured.Unstructured).DeepCopy()
			return nil
		}).
		Once()
	result, err := s.testObj.Process(ctx, inst)

	// Both resources were Created → immediate requeue without checking HelmRelease conditions.
	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
	s.Require().NotNil(capturedHR)
	vals, found, _ := unstructured.NestedMap(capturedHR.Object, "spec", "values")
	s.Assert().True(found)
	s.Assert().EqualValues(float64(2), vals["replicaCount"])
}

func (s *DeployTestSuite) TestProcess_ExistingResourcesUpdated() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// OCIRepository already exists → Update
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.SetResourceVersion("1")
			return nil
		}).
		Once()
	s.clientMock.EXPECT().
		Update(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()
	// HelmRelease already exists → Update
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.SetResourceVersion("2")
			return nil
		}).
		Once()
	s.clientMock.EXPECT().
		Update(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	// Both resources were Updated → immediate requeue, HelmRelease conditions not checked.
	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ResourcesCreated_ImmediateRequeue() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// Fresh Create for both resources → OperationResultCreated → immediate StopWithRequeue.
	s.mockCreateOrUpdate("wildwest-controller", "providers-wildwest-ns") // OCIRepository
	s.mockCreateOrUpdate("wildwest-controller", "providers-wildwest-ns") // HelmRelease

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_GenerationMismatch_Requeue() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	ocm := inst.Spec.RuntimeDeployments[0].OCM
	ociURL := fmt.Sprintf("oci://%s/%s", ocm.Registry, ocm.ComponentName)

	// OCIRepository exists but observedGeneration lags behind generation → Flux hasn't
	// fully processed the current spec yet.
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "wildwest-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			u.SetResourceVersion("1")
			u.SetGeneration(2)
			_ = unstructured.SetNestedField(u.Object, ociURL, "spec", "url")
			_ = unstructured.SetNestedField(u.Object, ocm.Version, "spec", "ref", "tag")
			_ = unstructured.SetNestedField(u.Object, "generic", "spec", "provider")
			_ = unstructured.SetNestedField(u.Object, "1m0s", "spec", "interval")
			_ = unstructured.SetNestedField(u.Object, ocm.Insecure, "spec", "insecure")
			_ = unstructured.SetNestedMap(u.Object, map[string]interface{}{
				"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				"operation": "copy",
			}, "spec", "layerSelector")
			_ = unstructured.SetNestedField(u.Object, int64(1), "status", "observedGeneration") // lag
			return nil
		}).Once()
	s.mockExistingNoopHelmRelease("wildwest-controller", "providers-wildwest-ns")

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal(providersv1alpha1.ManagedProviderPhaseDeploying, inst.Status.Phase)
}

// --- Finalize tests ---

func (s *DeployTestSuite) TestFinalize_ControllerOnly_Requeues() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// HelmRelease and OCIRepository delete both succeed → still pending deletion → requeue
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Times(2)

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.clientMock.AssertExpectations(s.T())
}

func (s *DeployTestSuite) TestFinalize_WithPortal_Requeues() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()

	// wildwest-controller HelmRelease, wildwest-controller OCIRepository,
	// wildwest-portal HelmRelease, wildwest-portal OCIRepository
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Times(4)

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.clientMock.AssertExpectations(s.T())
}

func (s *DeployTestSuite) TestFinalize_AllAlreadyGone() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// Both resources already gone → allGone = true → OK
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "wildwest-controller")).
		Times(2)

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
}

func (s *DeployTestSuite) TestFinalize_HelmReleaseDeleteFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(errors.New("delete failed")).
		Once()

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to delete HelmRelease")
}

func (s *DeployTestSuite) TestFinalize_OCIRepoDeleteFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// HelmRelease delete OK, OCIRepository delete fails
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(errors.New("delete failed")).
		Once()

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to delete OCIRepository")
}
