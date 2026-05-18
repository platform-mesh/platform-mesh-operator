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
	"testing"

	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

type WaitPlatformMeshTestSuite struct {
	suite.Suite
	testObj    *WaitPlatformMeshSubroutine
	clientMock *mocks.Client
	log        *logger.Logger
}

func TestWaitPlatformMeshTestSuite(t *testing.T) {
	suite.Run(t, new(WaitPlatformMeshTestSuite))
}

func (s *WaitPlatformMeshTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "WaitPlatformMeshTestSuite"
	s.log, _ = logger.New(cfg)

	s.clientMock = new(mocks.Client)
	s.clientMock.EXPECT().Scheme().Return(runtime.NewScheme()).Maybe()

	s.testObj = NewWaitPlatformMeshSubroutine(s.clientMock)
}

func (s *WaitPlatformMeshTestSuite) TearDownTest() {
	s.clientMock = nil
	s.testObj = nil
}

func (s *WaitPlatformMeshTestSuite) newCtx() context.Context {
	return context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
}

func (s *WaitPlatformMeshTestSuite) newManagedProvider() *providersv1alpha1.ManagedProvider {
	return &providersv1alpha1.ManagedProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-provider",
			Namespace: "my-namespace",
		},
		Spec: providersv1alpha1.ManagedProviderSpec{
			PlatformMeshReference: providersv1alpha1.PlatformMeshReferenceSpec{
				Name: "my-platform-mesh",
			},
		},
	}
}

// --- Process ---

func (s *WaitPlatformMeshTestSuite) TestProcess_PlatformMeshNotFound() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "my-platform-mesh", Namespace: "my-namespace"}, mock.AnythingOfType("*v1alpha1.PlatformMesh")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "platformmeshes"}, "my-platform-mesh"))

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
}

func (s *WaitPlatformMeshTestSuite) TestProcess_PlatformMeshNotReady() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "my-platform-mesh", Namespace: "my-namespace"}, mock.AnythingOfType("*v1alpha1.PlatformMesh")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			obj.(*corev1alpha1.PlatformMesh).Status.Conditions = []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse},
			}
			return nil
		})

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
}

func (s *WaitPlatformMeshTestSuite) TestProcess_PlatformMeshNoConditions() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "my-platform-mesh", Namespace: "my-namespace"}, mock.AnythingOfType("*v1alpha1.PlatformMesh")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			// no conditions set
			return nil
		})

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
}

func (s *WaitPlatformMeshTestSuite) TestProcess_PlatformMeshReady() {
	ctx := s.newCtx()
	inst := s.newManagedProvider()

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "my-platform-mesh", Namespace: "my-namespace"}, mock.AnythingOfType("*v1alpha1.PlatformMesh")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			obj.(*corev1alpha1.PlatformMesh).Status.Conditions = []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			}
			return nil
		})

	result, err := s.testObj.Process(ctx, inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
}
