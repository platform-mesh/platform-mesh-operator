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
	s.log, _ = logger.New(logger.DefaultConfig())
	s.testObj = subroutines.NewFeatureToggleSubroutine(s.clientMock, s.helperMock, &config.OperatorConfig{
		WorkspaceDir: "../..",
	}, "https://kcp.example.com")
}

func (s *FeaturesTestSuite) TearDownTest() {
	s.clientMock = nil
	s.helperMock = nil
	s.testObj = nil
}

func (s *FeaturesTestSuite) TestProcess() {
	operatorCfg := config.OperatorConfig{}

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)

	// Mock the kubeconfig secret lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "",
			Namespace: "",
		}, mock.Anything).
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

	// Mock patch calls for applying manifests (flexible count)
	mockKcpClient.EXPECT().
		Patch(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything, mock.Anything).
		Return(nil).Times(100)

	// Expect multiple Patch calls for applying manifests (flexible count)
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(100)

	// Mock workspace lookups and patch calls
	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		})

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
