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
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

type ScopedKubeconfigTestSuite struct {
	suite.Suite
	testObj *ScopedKubeconfigSubroutine
	clMock  *mocks.Client
	log     *logger.Logger
}

func TestScopedKubeconfigTestSuite(t *testing.T) {
	suite.Run(t, new(ScopedKubeconfigTestSuite))
}

func (s *ScopedKubeconfigTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "ScopedKubeconfigTestSuite"
	s.log, _ = logger.New(cfg)

	s.clMock = new(mocks.Client)
	s.clMock.EXPECT().Scheme().Return(runtime.NewScheme()).Maybe()

	s.testObj = NewScopedKubeconfigSubroutine(
		"https://kcp.api.example.com",
		func(_ context.Context) (client.Client, error) {
			return s.clMock, nil
		},
	)
}

func (s *ScopedKubeconfigTestSuite) TearDownTest() {
	s.clMock = nil
	s.testObj = nil
}

func (s *ScopedKubeconfigTestSuite) newCtx() context.Context {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	return mccontext.WithCluster(ctx, multicluster.ClusterName("root:providers:wildwest"))
}

func (s *ScopedKubeconfigTestSuite) newProvider() *providersv1alpha1.Provider {
	return &providersv1alpha1.Provider{
		ObjectMeta: metav1.ObjectMeta{Name: "wildwest"},
	}
}

// mockPreTokenSteps sets up successful mocks for namespace, SA, Role, and
// RoleBinding — everything that runs before the token Secret CreateOrUpdate.
func (s *ScopedKubeconfigTestSuite) mockPreTokenSteps() {
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).
		Return(nil)
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.ServiceAccount"), mock.Anything).
		Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Role")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "roles"}, "platform-mesh-provider-wildwest"))
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Role"), mock.Anything).
		Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.RoleBinding")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "rolebindings"}, "platform-mesh-provider-wildwest"))
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.RoleBinding"), mock.Anything).
		Return(nil)
}

// --- Process ---

func (s *ScopedKubeconfigTestSuite) TestProcess_ClusterNameNotInContext() {
	// Context has no cluster name key; getClusterFromContext still succeeds.
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	result, err := s.testObj.Process(ctx, s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "failed to get cluster from context")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_NamespaceCreateFails() {
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).
		Return(errors.New("server error"))

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "ensure namespace")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_NamespaceAlreadyExists_Continues() {
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).
		Return(kerrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, "default"))
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.ServiceAccount"), mock.Anything).
		Return(errors.New("stop here"))

	_, err := s.testObj.Process(s.newCtx(), s.newProvider())

	// AlreadyExists on namespace is ignored; execution continues to SA creation.
	s.Require().Error(err)
	s.Assert().Contains(err.Error(), "create ServiceAccount")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_SACreateFails() {
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).
		Return(nil)
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.ServiceAccount"), mock.Anything).
		Return(errors.New("forbidden"))

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "create ServiceAccount")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_RoleCreateOrUpdateFails() {
	s.clMock.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).Return(nil)
	s.clMock.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1.ServiceAccount"), mock.Anything).Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Role")).
		Return(errors.New("server error"))

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "create or update Role")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_RoleBindingCreateOrUpdateFails() {
	s.clMock.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1.Namespace"), mock.Anything).Return(nil)
	s.clMock.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1.ServiceAccount"), mock.Anything).Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Role")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "roles"}, "platform-mesh-provider-wildwest"))
	s.clMock.EXPECT().Create(mock.Anything, mock.AnythingOfType("*v1.Role"), mock.Anything).Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.RoleBinding")).
		Return(errors.New("server error"))

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "create or update RoleBinding")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_TokenSecretCreateOrUpdateFails() {
	s.mockPreTokenSteps()
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-token-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		Return(errors.New("server error"))

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
	s.Assert().Contains(err.Error(), "create or update token Secret")
}

func (s *ScopedKubeconfigTestSuite) TestProcess_TokenNotPopulatedYet() {
	s.mockPreTokenSteps()
	// Token secret Create succeeds but leaves Data empty (no SA token controller).
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-token-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "platform-mesh-provider-token-wildwest"))
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Secret"), mock.Anything).
		Return(nil)

	result, err := s.testObj.Process(s.newCtx(), s.newProvider())

	s.Require().NoError(err)
	s.Assert().True(result.IsStopWithRequeue())
}

func (s *ScopedKubeconfigTestSuite) TestProcess_HappyPath() {
	inst := s.newProvider()
	s.mockPreTokenSteps()
	// Token secret: already exists with token data populated → Update.
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-token-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.ResourceVersion = "1"
			secret.Data = map[string][]byte{
				"token":  []byte("fake-token"),
				"ca.crt": []byte("fake-ca"),
			}
			return nil
		})
	s.clMock.EXPECT().
		Update(mock.Anything, mock.AnythingOfType("*v1.Secret"), mock.Anything).
		Return(nil)
	// Kubeconfig secret: NotFound → Create.
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-kubeconfig-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "platform-mesh-provider-kubeconfig-wildwest"))
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Secret"), mock.Anything).
		Return(nil)

	result, err := s.testObj.Process(s.newCtx(), inst)

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
	s.Require().NotNil(inst.Status.KubeconfigSecretRef)
	s.Assert().Equal("platform-mesh-provider-kubeconfig-wildwest", inst.Status.KubeconfigSecretRef.Name)
	s.Assert().Equal("default", inst.Status.KubeconfigSecretRef.Namespace)
	s.Assert().Equal("Ready", inst.Status.Phase)
}

func (s *ScopedKubeconfigTestSuite) TestProcess_HostOverride() {
	inst := s.newProvider()
	inst.Spec.HostOverride = "https://custom.kcp.host"

	s.mockPreTokenSteps()
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-token-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.ResourceVersion = "1"
			secret.Data = map[string][]byte{"token": []byte("t"), "ca.crt": []byte("ca")}
			return nil
		})
	s.clMock.EXPECT().Update(mock.Anything, mock.AnythingOfType("*v1.Secret"), mock.Anything).Return(nil)
	s.clMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "platform-mesh-provider-kubeconfig-wildwest", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		Return(kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "platform-mesh-provider-kubeconfig-wildwest"))

	var capturedSecret *corev1.Secret
	s.clMock.EXPECT().
		Create(mock.Anything, mock.AnythingOfType("*v1.Secret"), mock.Anything).
		RunAndReturn(func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
			capturedSecret = obj.(*corev1.Secret)
			return nil
		})

	_, err := s.testObj.Process(s.newCtx(), inst)

	s.Require().NoError(err)
	s.Require().NotNil(capturedSecret)
	s.Assert().Contains(string(capturedSecret.Data["kubeconfig"]), "https://custom.kcp.host")
}

// --- Finalize ---

func (s *ScopedKubeconfigTestSuite) TestFinalize_DeleteFails() {
	s.clMock.EXPECT().
		Delete(mock.Anything, mock.AnythingOfType("*v1.RoleBinding"), mock.Anything).
		Return(errors.New("delete failed"))

	result, err := s.testObj.Finalize(s.newCtx(), s.newProvider())

	s.Require().Error(err)
	s.Assert().True(result.IsContinue())
}

func (s *ScopedKubeconfigTestSuite) TestFinalize_NotFoundIgnored() {
	notFound := kerrors.NewNotFound(schema.GroupResource{Resource: "test"}, "wildwest")
	s.clMock.EXPECT().
		Delete(mock.Anything, mock.Anything, mock.Anything).
		Return(notFound).Times(5)

	result, err := s.testObj.Finalize(s.newCtx(), s.newProvider())

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
}

func (s *ScopedKubeconfigTestSuite) TestFinalize_HappyPath() {
	s.clMock.EXPECT().
		Delete(mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Times(5)

	result, err := s.testObj.Finalize(s.newCtx(), s.newProvider())

	s.Require().NoError(err)
	s.Assert().True(result.IsContinue())
}
