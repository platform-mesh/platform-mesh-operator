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

	providersv1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/providers/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

type WorkspaceTestSuite struct {
	suite.Suite
	testObj       *WorkspaceSubroutine
	clientMock    *mocks.Client
	kcpHelperMock *mocks.KcpHelper
	kcpClientMock *mocks.Client
	log           *logger.Logger
	operatorCfg   config.OperatorConfig
}

func TestWorkspaceTestSuite(t *testing.T) {
	suite.Run(t, new(WorkspaceTestSuite))
}

func (s *WorkspaceTestSuite) SetupTest() {
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "WorkspaceTestSuite"
	s.log, _ = logger.New(cfg)

	s.clientMock = new(mocks.Client)
	s.kcpHelperMock = new(mocks.KcpHelper)
	s.kcpClientMock = new(mocks.Client)

	s.clientMock.EXPECT().Scheme().Return(runtime.NewScheme()).Maybe()

	s.operatorCfg = config.OperatorConfig{}
	s.operatorCfg.KCP.ClusterAdminSecretName = "kcp-admin"
	s.operatorCfg.KCP.Namespace = "platform-mesh-system"

	var err error
	s.testObj, err = NewWorkspaceSubroutine(s.clientMock, s.kcpHelperMock, &s.operatorCfg, "https://kcp.api.example.com")
	s.Require().NoError(err)
}

func (s *WorkspaceTestSuite) TearDownTest() {
	s.clientMock = nil
	s.kcpHelperMock = nil
	s.kcpClientMock = nil
	s.testObj = nil
}

func (s *WorkspaceTestSuite) newCtx() context.Context {
	return context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
}

func (s *WorkspaceTestSuite) newManagedProvider() *providersv1alpha1.ManagedProvider {
	return &providersv1alpha1.ManagedProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cowboys",
			Namespace: "providers-wildwest-ns",
		},
	}
}

func (s *WorkspaceTestSuite) mockAdminSecret() {
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-admin", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt":  []byte("fake-ca"),
				"tls.crt": []byte("fake-cert"),
				"tls.key": []byte("fake-key"),
			}
			return nil
		})
}

// --- Process ---

func (s *WorkspaceTestSuite) TestProcess() {
	cases := []struct {
		name            string
		mutate          func(*providersv1alpha1.ManagedProvider)
		setup           func()
		wantErrContains string
	}{
		{
			name:            "invalid workspace path",
			mutate:          func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.WorkspacePath = "nocolon" },
			wantErrContains: "invalid workspace path",
		},
		{
			name: "build kcp admin config fails",
			setup: func() {
				s.clientMock.EXPECT().
					Get(mock.Anything, types.NamespacedName{Name: "kcp-admin", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
					Return(errors.New("connection refused"))
			},
			wantErrContains: "failed to build kcp admin config",
		},
		{
			name: "new kcp client fails",
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(nil, errors.New("dial error"))
			},
			wantErrContains: "failed to create kcp client",
		},
		{
			name: "apply workspace fails",
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Apply(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("apply failed"))
			},
			wantErrContains: "failed to apply workspace",
		},
		{
			name: "happy path default workspace path",
			setup: func() {
				s.mockAdminSecret()
				// root:providers:cowboys → parent root:providers
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Apply(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil)
			},
		},
		{
			name:   "happy path custom workspace path",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.WorkspacePath = "root:custom:providers:cowboys" },
			setup: func() {
				s.mockAdminSecret()
				// root:custom:providers:cowboys → parent root:custom:providers
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:custom:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Apply(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			s.SetupTest()
			inst := s.newManagedProvider()
			if tc.mutate != nil {
				tc.mutate(inst)
			}
			if tc.setup != nil {
				tc.setup()
			}

			result, err := s.testObj.Process(s.newCtx(), inst)

			if tc.wantErrContains != "" {
				s.Require().Error(err)
				s.Assert().Contains(err.Error(), tc.wantErrContains)
				s.Assert().True(result.IsContinue())
			} else {
				s.Require().NoError(err)
				s.Assert().True(result.IsContinue())
			}
		})
	}
}

// --- Finalize ---

func (s *WorkspaceTestSuite) TestFinalize() {
	cases := []struct {
		name            string
		mutate          func(*providersv1alpha1.ManagedProvider)
		setup           func()
		wantErrContains string
		wantRequeue     bool
	}{
		{
			name: "cleanup on delete false",
			// CleanupOnDelete defaults to false → early return, no mocks needed
		},
		{
			name:   "build kcp admin config fails",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.CleanupOnDelete = true },
			setup: func() {
				s.clientMock.EXPECT().
					Get(mock.Anything, types.NamespacedName{Name: "kcp-admin", Namespace: "platform-mesh-system"}, mock.AnythingOfType("*v1.Secret")).
					Return(errors.New("connection refused"))
			},
			wantErrContains: "failed to build kcp admin config",
		},
		{
			name:   "new kcp client fails",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.CleanupOnDelete = true },
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(nil, errors.New("dial error"))
			},
			wantErrContains: "failed to create kcp client",
		},
		{
			name:   "delete fails",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.CleanupOnDelete = true },
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Delete(mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace"), mock.Anything).
					Return(errors.New("delete failed"))
			},
			wantErrContains: "failed to delete workspace",
		},
		{
			name:   "workspace already deleted (not found)",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.CleanupOnDelete = true },
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Delete(mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace"), mock.Anything).
					Return(kerrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, "cowboys"))
			},
			wantRequeue: false,
		},
		{
			name:   "delete issued, requeue to wait for deletion",
			mutate: func(inst *providersv1alpha1.ManagedProvider) { inst.Spec.CleanupOnDelete = true },
			setup: func() {
				s.mockAdminSecret()
				s.kcpHelperMock.EXPECT().NewKcpClient(mock.Anything, "root:providers").
					Return(s.kcpClientMock, nil)
				s.kcpClientMock.EXPECT().Delete(mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace"), mock.Anything).
					Return(nil)
			},
			wantRequeue: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		s.Run(tc.name, func() {
			s.SetupTest()
			inst := s.newManagedProvider()
			if tc.mutate != nil {
				tc.mutate(inst)
			}
			if tc.setup != nil {
				tc.setup()
			}

			result, err := s.testObj.Finalize(s.newCtx(), inst)

			if tc.wantErrContains != "" {
				s.Require().Error(err)
				s.Assert().Contains(err.Error(), tc.wantErrContains)
				s.Assert().True(result.IsContinue())
			} else {
				s.Require().NoError(err)
				if tc.wantRequeue {
					s.Assert().True(result.IsStopWithRequeue())
				} else {
					s.Assert().True(result.IsContinue())
				}
			}
		})
	}
}
