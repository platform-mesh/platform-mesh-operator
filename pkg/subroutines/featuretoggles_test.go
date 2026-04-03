package subroutines

import (
	"context"
	"testing"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	"github.com/platform-mesh/subroutines"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FeaturesTestSuite struct {
	suite.Suite
	clientMock *mocks.Client
	helperMock *mocks.KcpHelper
	testObj    *FeatureToggleSubroutine
	log        *logger.Logger
}

func TestFeaturesTestSuite(t *testing.T) {
	suite.Run(t, new(FeaturesTestSuite))
}

func (s *FeaturesTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "FeaturesTestSuite"
	s.log, _ = logger.New(cfg)
	s.testObj = NewFeatureToggleSubroutine(s.clientMock, s.helperMock, &config.OperatorConfig{
		WorkspaceDir: "../..",
	}, "https://kcp.example.com")
}

func (s *FeaturesTestSuite) TearDownTest() {
	s.clientMock = nil
	s.helperMock = nil
	s.testObj = nil
}

func (s *FeaturesTestSuite) resetFeatureToggleTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	s.testObj = NewFeatureToggleSubroutine(s.clientMock, s.helperMock, &config.OperatorConfig{
		WorkspaceDir: "../..",
	}, "https://kcp.example.com")
}

// setupFeatureToggleApplyMocks configures clients for one or more
// applyKcpManifests executions
func (s *FeaturesTestSuite) setupFeatureToggleApplyMocks(
	operatorCfg config.OperatorConfig, applyKcpManifestsCalls int,
) *mocks.Client {
	secretGetCount := applyKcpManifestsCalls * 2 // (once in applyKcpManifests, once in buildKubeconfig)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      operatorCfg.KCP.ClusterAdminSecretName,
			Namespace: operatorCfg.KCP.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt":  []byte("test-ca-data"),
				"tls.crt": []byte("test-tls-crt"),
				"tls.key": []byte("test-tls-key"),
			}
			return nil
		}).
		Times(secretGetCount)

	mockKcpClient := new(mocks.Client)
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, mock.Anything).
		Return(mockKcpClient, nil).
		Maybe()
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs:default").
		Return(mockKcpClient, nil).
		Maybe()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstructuredObj := obj.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Ready",
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Available",
							"status": "True",
						},
					},
				},
			}
			return nil
		}).
		Maybe()

	mockKcpClient.EXPECT().Apply(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		}).
		Maybe()

	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, "")).
		Maybe()

	return mockKcpClient
}

func testOperatorCfg() config.OperatorConfig {
	operatorCfg := config.OperatorConfig{}
	operatorCfg.KCP.RootShardName = "root-shard"
	operatorCfg.KCP.FrontProxyName = "front-proxy"
	operatorCfg.KCP.Namespace = "kcp-system"
	operatorCfg.KCP.ClusterAdminSecretName = "kcp-admin-kubeconfig"
	return operatorCfg
}

func (s *FeaturesTestSuite) TestProcess() {
	operatorCfg := testOperatorCfg()
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)

	manifestBackedToggles := []struct {
		name   string
		toggle string
	}{
		{"feature-enable-getting-started", "feature-enable-getting-started"},
		{"feature-enable-marketplace-account", "feature-enable-marketplace-account"},
		{"feature-enable-marketplace-org", "feature-enable-marketplace-org"},
		{"feature-accounts-in-accounts", "feature-accounts-in-accounts"},
		{"feature-enable-account-iam-ui", "feature-enable-account-iam-ui"},
		{"feature-enable-terminal-controller-manager", "feature-enable-terminal-controller-manager"},
	}

	for _, tc := range manifestBackedToggles {
		s.Run(tc.name, func() {
			s.resetFeatureToggleTest()
			s.setupFeatureToggleApplyMocks(operatorCfg, 1)

			result, err := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{
				Spec: corev1alpha1.PlatformMeshSpec{
					FeatureToggles: []corev1alpha1.FeatureToggle{
						{Name: tc.toggle, Parameters: map[string]string{}},
					},
				},
			})
			s.Assert().NoError(err)
			s.Assert().Equal(subroutines.OK(), result)
		})
	}

	s.Run("all manifest-backed toggles in one reconcile", func() {
		s.resetFeatureToggleTest()
		s.setupFeatureToggleApplyMocks(operatorCfg, len(manifestBackedToggles))
		toggles := make([]corev1alpha1.FeatureToggle, 0, len(manifestBackedToggles))
		for _, tc := range manifestBackedToggles {
			toggles = append(toggles, corev1alpha1.FeatureToggle{
				Name: tc.toggle, Parameters: map[string]string{},
			})
		}
		result, err := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{
			Spec: corev1alpha1.PlatformMeshSpec{FeatureToggles: toggles},
		})
		s.Assert().NoError(err)
		s.Assert().Equal(subroutines.OK(), result)
	})

	s.Run("feature-disable-email-verification", func() {
		s.resetFeatureToggleTest()
		result, err := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{
			Spec: corev1alpha1.PlatformMeshSpec{
				FeatureToggles: []corev1alpha1.FeatureToggle{
					{Name: "feature-disable-email-verification"},
				},
			},
		})
		s.Assert().NoError(err)
		s.Assert().Equal(subroutines.OK(), result)
	})

	s.Run("unknown feature toggle hits default branch", func() {
		s.resetFeatureToggleTest()
		result, err := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{
			Spec: corev1alpha1.PlatformMeshSpec{
				FeatureToggles: []corev1alpha1.FeatureToggle{
					{Name: "unknown-toggle-name"},
				},
			},
		})
		s.Assert().NoError(err)
		s.Assert().Equal(subroutines.OK(), result)
	})
}
