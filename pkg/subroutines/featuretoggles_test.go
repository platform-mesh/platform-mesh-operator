package subroutines_test

import (
	"context"
	"testing"

	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	subroutines "github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FeaturesTestSuite struct {
	suite.Suite
	clientMock *mocks.Client
	helperMock *mocks.KcpHelper
	testObj    *subroutines.FeatureToggleSubroutine
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
	s.testObj = subroutines.NewFeatureToggleSubroutine(s.clientMock, s.helperMock, &config.OperatorConfig{
		WorkspaceDir: "../..",
	})
}

func (s *FeaturesTestSuite) TearDownTest() {
	s.clientMock = nil
	s.helperMock = nil
	s.testObj = nil
}

func (s *FeaturesTestSuite) TestProcess() {
	operatorCfg := config.OperatorConfig{}
	operatorCfg.KCP.RootShardName = "root-shard"
	operatorCfg.KCP.FrontProxyName = "front-proxy"
	operatorCfg.KCP.Namespace = "kcp-system"
	operatorCfg.KCP.ClusterAdminSecretName = "kcp-admin-kubeconfig"

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)

	// Mock the kubeconfig secret lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "kcp-admin-kubeconfig",
			Namespace: "kcp-system",
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt":  []byte("test-ca-data"),
				"tls.crt": []byte("test-tls-crt"),
				"tls.key": []byte("test-tls-key"),
			}
			return nil
		})

	// Create mock KCP client
	mockKcpClient := new(mocks.Client)
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, mock.Anything).
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs:default").
		Return(mockKcpClient, nil)

	// Mock unstructured object lookups (for general manifest objects - flexible count)
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
		})

	// Expect multiple Patch calls for applying manifests (flexible count)
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock workspace lookups and patch calls
	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		})

	mockKcpClient.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "main-home-overview-getting-started"}, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{Group: "tenancy.kcp.io", Resource: "workspaces"}, "main-home-overview-getting-started"))

	// Call Process
	result, opErr := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{
		Spec: corev1alpha1.PlatformMeshSpec{
			FeatureToggles: []corev1alpha1.FeatureToggle{
				{Name: "feature-enable-getting-started", Parameters: map[string]string{}},
			},
		},
	})

	// Assertions
	s.Assert().Nil(opErr)
	s.Assert().Equal(ctrl.Result{}, result)

}
