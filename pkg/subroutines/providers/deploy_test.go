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

	s.testObj = NewDeploySubroutine(s.clientMock)
}

func (s *DeployTestSuite) TearDownTest() {
	s.clientMock = nil
	s.testObj = nil
}

func (s *DeployTestSuite) newCtx() context.Context {
	return context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
}

func (s *DeployTestSuite) newManagedProvider() *providersv1alpha1.ManagedProvider {
	return &providersv1alpha1.ManagedProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cowboys",
			Namespace: "providers-wildwest-ns",
		},
		Spec: providersv1alpha1.ManagedProviderSpec{
			Controller: providersv1alpha1.ProviderComponentSpec{
				OCM: providersv1alpha1.OCMComponentSpec{
					ComponentName: "github.com/platform-mesh/wildwest-controller",
					Version:       "0.1.0",
					Registry:      "ghcr.io/platform-mesh/ocm",
				},
			},
		},
	}
}

func (s *DeployTestSuite) newManagedProviderWithPortal() *providersv1alpha1.ManagedProvider {
	inst := s.newManagedProvider()
	inst.Spec.Portal = &providersv1alpha1.ProviderComponentSpec{
		OCM: providersv1alpha1.OCMComponentSpec{
			ComponentName: "github.com/platform-mesh/cowboys-portal",
			Version:       "0.1.0",
			Registry:      "ghcr.io/platform-mesh/ocm",
		},
	}
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

// mockComponentDeployed sets up all mock calls for a single component that
// creates both resources fresh and reaches the given readiness state.
func (s *DeployTestSuite) mockComponentDeployed(name, namespace string, ready bool) {
	s.mockCreateOrUpdate(name, namespace) // OCIRepository
	s.mockCreateOrUpdate(name, namespace) // HelmRelease
	s.mockHelmReleaseReadyCheck(name, namespace, ready)
}

// --- Process tests ---

func (s *DeployTestSuite) TestProcess_OCIRepositoryCreateFails() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
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

	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // OCIRepository OK
	// HelmRelease Get → NotFound, Create → error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
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

	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // OCIRepository OK
	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // HelmRelease OK
	// helmReleaseReady Get → non-404 error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
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

	s.mockComponentDeployed("cowboys-controller", "providers-wildwest-ns", false)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal("Deploying", inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_NoPortal() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.mockComponentDeployed("cowboys-controller", "providers-wildwest-ns", true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Equal("Deployed", inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_PortalNotReady() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()

	s.mockComponentDeployed("cowboys-controller", "providers-wildwest-ns", true)
	s.mockComponentDeployed("cowboys-portal", "providers-wildwest-ns", false)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal("Deploying", inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_ControllerReady_PortalReady() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()

	s.mockComponentDeployed("cowboys-controller", "providers-wildwest-ns", true)
	s.mockComponentDeployed("cowboys-portal", "providers-wildwest-ns", true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Equal("Deployed", inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_HelmReleaseNotFoundDuringReadyCheck() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // OCIRepository
	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // HelmRelease
	// helmReleaseReady: NotFound → treated as not ready, no error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
		Once()

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
	s.Assert().Equal("Deploying", inst.Status.Phase)
}

func (s *DeployTestSuite) TestProcess_WithHelmValues() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()
	inst.Spec.Controller.OCM.Values = apiextensionsv1.JSON{
		Raw: []byte(`{"replicaCount":2,"image":{"tag":"v0.1.0"}}`),
	}

	// Capture the HelmRelease Create call to verify values are injected.
	var capturedHR *unstructured.Unstructured
	s.mockCreateOrUpdate("cowboys-controller", "providers-wildwest-ns") // OCIRepository
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
		Once()
	s.clientMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		RunAndReturn(func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
			capturedHR = obj.(*unstructured.Unstructured).DeepCopy()
			return nil
		}).
		Once()
	s.mockHelmReleaseReadyCheck("cowboys-controller", "providers-wildwest-ns", true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
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
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
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
		Get(mock.Anything, types.NamespacedName{Name: "cowboys-controller", Namespace: "providers-wildwest-ns"}, mock.AnythingOfType("*unstructured.Unstructured")).
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
	s.mockHelmReleaseReadyCheck("cowboys-controller", "providers-wildwest-ns", true)

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Equal("Deployed", inst.Status.Phase)
}

// --- Finalize tests ---

func (s *DeployTestSuite) TestFinalize_ControllerOnly() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	// HelmRelease delete
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()
	// OCIRepository delete
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Once()

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.clientMock.AssertExpectations(s.T())
}

func (s *DeployTestSuite) TestFinalize_WithPortal() {
	ctx := s.newCtx()
	inst := s.newManagedProviderWithPortal()

	// controller HelmRelease, controller OCIRepository, portal HelmRelease, portal OCIRepository
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(nil).
		Times(4)

	result, err := s.testObj.Finalize(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.clientMock.AssertExpectations(s.T())
}

func (s *DeployTestSuite) TestFinalize_NotFound_Ignored() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
		Once()
	s.clientMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything).
		Return(kerrors.NewNotFound(schema.GroupResource{}, "cowboys-controller")).
		Once()

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
