package subroutines_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/context/keys"
	"github.com/openmfp/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	"github.com/openmfp/openmfp-operator/pkg/subroutines/mocks"
)

var ManifestStructureTest = "../../manifests/kcp"

type KcpsetupTestSuite struct {
	suite.Suite
	clientMock *mocks.Client
	helperMock *mocks.KcpHelper
	testObj    *subroutines.KcpsetupSubroutine
	log        *logger.Logger
}

func TestKcpsetupTestSuite(t *testing.T) {
	suite.Run(t, new(KcpsetupTestSuite))
}

func (s *KcpsetupTestSuite) Test_applyDirStructure() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	kcpClientMock := new(mocks.Client)
	s.helperMock.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(kcpClientMock, nil)
	inventory := map[string]string{
		"apiExportRootTenancyKcpIoIdentityHash":  "hash1",
		"apiExportRootShardsKcpIoIdentityHash":   "hash2",
		"apiExportRootTopologyKcpIoIdentityHash": "hash3",
	}
	kcpClientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	kcpClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			workspace := obj.(*kcptenancyv1alpha.Workspace)
			workspace.Status.Phase = "Ready"
			return nil
		})

	err := s.testObj.ApplyDirStructure(ctx, "../../manifests/kcp", "root", &rest.Config{}, inventory, &corev1alpha1.OpenMFP{})

	s.Assert().Nil(err)

}

func (s *KcpsetupTestSuite) Test_getCABundleInventory() {
	s.T().Skip("Skipping test temporarily")
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	expectedCaData := []byte("test-ca-data")

	// Test case 1: Success case
	// Default webhook secret - called once since we cache results
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretData: expectedCaData,
			}
			return nil
		}).
		Once() // Only called once due to caching

	// Secondary webhook secret - called once since we cache results
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "account-operator-webhook-server-cert",
			Namespace: "openmfp-system",
		}, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt": expectedCaData,
			}
			return nil
		}).
		Once() // Only called once due to caching

	// First call should fetch from secrets
	inventory, err := s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err)
	s.Assert().NotNil(inventory)

	// Check default webhook CA bundle
	defaultKey := subroutines.DEFAULT_WEBHOOK_CONFIGURATION.WebhookRef.Name + ".ca-bundle"
	s.Assert().Contains(inventory, defaultKey)
	s.Assert().Equal(string(expectedCaData), inventory[defaultKey])

	// Check secondary webhook CA bundle
	secondaryKey := "account-operator.webhooks.core.platform-mesh.io.ca-bundle"
	s.Assert().Contains(inventory, secondaryKey)
	s.Assert().Equal(string(expectedCaData), inventory[secondaryKey])

	// Second call should use cache
	inventory, err = s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err)
	s.Assert().NotNil(inventory)
	s.Assert().Contains(inventory, defaultKey)
	s.Assert().Contains(inventory, secondaryKey)
	s.Assert().Equal(string(expectedCaData), inventory[defaultKey])
	s.Assert().Equal(string(expectedCaData), inventory[secondaryKey])

	s.clientMock.AssertExpectations(s.T())

	// Test case 2: Secret not found
	// Create a new instance to clear the cache
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, &subroutines.Helper{}, ManifestStructureTest, "")

	// Default webhook secret - called once since we cache results
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.Anything).
		Return(errors.New("secret not found")).
		Once() // Only called once due to caching

	inventory, err = s.testObj.GetCABundleInventory(ctx)
	s.Assert().Error(err)
	s.Assert().Nil(inventory)
	s.Assert().Contains(err.Error(), "Failed to get CA bundle")
	s.clientMock.AssertExpectations(s.T())
}

func (s *KcpsetupTestSuite) Test_GetCaBundle() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	webhookConfig := &corev1alpha1.WebhookConfiguration{
		SecretRef: corev1alpha1.SecretReference{
			Name:      "ca-secret",
			Namespace: "default",
		},
		SecretData: "ca.crt",
	}
	expectedCaData := []byte("test-ca-data")

	// Test case 1: Successful retrieval
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "ca-secret", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt": expectedCaData,
			}
			return nil
		}).Once()

	caData, err := s.testObj.GetCaBundle(ctx, webhookConfig)
	s.Assert().NoError(err)
	s.Assert().Equal(expectedCaData, caData)
	s.clientMock.AssertExpectations(s.T())

	// Test case 2: Secret not found
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "ca-secret", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		Return(errors.New("secret not found")).Once()

	caData, err = s.testObj.GetCaBundle(ctx, webhookConfig)
	s.Assert().Error(err)
	s.Assert().Nil(caData)
	s.Assert().Contains(err.Error(), "Failed to get ca secret")
	s.clientMock.AssertExpectations(s.T())

	// Test case 3: Secret data key not found
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Name: "ca-secret", Namespace: "default"}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"wrong-key": []byte("some data"),
			}
			return nil
		}).Once()

	caData, err = s.testObj.GetCaBundle(ctx, webhookConfig)
	s.Assert().Error(err)
	s.Assert().Nil(caData)
	s.Assert().Contains(err.Error(), "Failed to get caData from secret")
	s.clientMock.AssertExpectations(s.T())
}

func (s *KcpsetupTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	s.log, _ = logger.New(logger.DefaultConfig())
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, s.helperMock, ManifestStructureTest, "https://kcp.example.com")
}

func (s *KcpsetupTestSuite) TearDownTest() {
	s.clientMock = nil
	s.helperMock = nil
	s.testObj = nil
}

func (s *KcpsetupTestSuite) TestProcess() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	// Mock the Helm release lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp", Namespace: "default"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			release := obj.(*unstructured.Unstructured)
			release.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Ready",
							"status": "True",
						},
					},
				},
			}
			return nil
		})

	// Mock the kubeconfig secret lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "kcp-cluster-admin-client-cert",
			Namespace: "openmfp-system",
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

	// Mock the webhook server cert lookup (called once since we cache results)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretData: []byte("test-ca-data"),
			}
			return nil
		}).Once() // Only called once due to caching

	// Mock the secondary webhook server cert lookup (called once since we cache results)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "account-operator-webhook-server-cert",
			Namespace: "openmfp-system",
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				"ca.crt": []byte("test-ca-data"),
			}
			return nil
		})

	// Create mock KCP client for APIExport lookups
	mockKcpClient := new(mocks.Client)
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:openmfp-system").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs:openmfp").
		Return(mockKcpClient, nil)

	// Mock APIExport lookups
	apiexport := &kcpapiv1alpha.APIExport{
		Status: kcpapiv1alpha.APIExportStatus{
			IdentityHash: "test-hash",
		},
	}

	// Mock all APIExport lookups
	mockKcpClient.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "tenancy.kcp.io"}, mock.AnythingOfType("*v1alpha1.APIExport")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			export := obj.(*kcpapiv1alpha.APIExport)
			export.Status = apiexport.Status
			return nil
		})

	mockKcpClient.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "shards.core.kcp.io"}, mock.AnythingOfType("*v1alpha1.APIExport")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			export := obj.(*kcpapiv1alpha.APIExport)
			export.Status = apiexport.Status
			return nil
		})

	mockKcpClient.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "topology.kcp.io"}, mock.AnythingOfType("*v1alpha1.APIExport")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			export := obj.(*kcpapiv1alpha.APIExport)
			export.Status = apiexport.Status
			return nil
		})

	// Mock workspace lookups and patch calls
	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		})

	mockKcpClient.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "orgs"}, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		})

	mockKcpClient.EXPECT().
		Patch(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything, mock.Anything).
		Return(nil)

	// Call Process
	result, opErr := s.testObj.Process(ctx, &corev1alpha1.OpenMFP{})

	// Assertions
	s.Assert().Nil(opErr)
	s.Assert().Equal(ctrl.Result{}, result)

	// Test error case - create a new instance to clear the cache
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, s.helperMock, ManifestStructureTest, "https://kcp.example.com")
}

func (s *KcpsetupTestSuite) Test_getAPIExportHashInventory() {
	// mocks
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Times(3)
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, ManifestStructureTest, "")

	apiexport := &kcpapiv1alpha.APIExport{
		Status: kcpapiv1alpha.APIExportStatus{
			IdentityHash: "hash1",
		},
	}
	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return nil
		}).Times(2)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return errors.New("error")
		}).Once()

	inventory, err := s.testObj.GetAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(map[string]string{
		"apiExportRootTenancyKcpIoIdentityHash": "hash1",
		"apiExportRootShardsKcpIoIdentityHash":  "hash1",
	}, inventory)

	// test error 2
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return nil
		}).Once()
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return errors.New("error")
		}).Once()

	inventory, err = s.testObj.GetAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(map[string]string{
		"apiExportRootTenancyKcpIoIdentityHash": "hash1",
	}, inventory)

	// test error 3
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return errors.New("error")
		}).Once()

	inventory, err = s.testObj.GetAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(map[string]string{}, inventory)

	// test error 4
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(nil, errors.New("Error")).Once()
	inventory, err = s.testObj.GetAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(map[string]string{}, inventory)
}

func (s *KcpsetupTestSuite) Test_Constructor() {
	// create new logger
	s.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	s.clientMock = new(mocks.Client)
	helper := &subroutines.Helper{}

	// create new test object
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, helper, ManifestStructureTest, "")
}

func (s *KcpsetupTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers()
	s.Assert().Equal(res, []string{subroutines.ProvidersecretSubroutineFinalizer})
}

func (s *KcpsetupTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.KcpsetupSubroutineName)
}

func (s *KcpsetupTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *KcpsetupTestSuite) TestApplyManifestFromFile() {

	cl := new(mocks.Client)
	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err := s.testObj.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-openmfp-system.yaml", cl, make(map[string]string), "root:openmfp-system", &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)

	err = s.testObj.ApplyManifestFromFile(context.TODO(), "invalid", nil, make(map[string]string), "root:openmfp-system", &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)

	err = s.testObj.ApplyManifestFromFile(context.TODO(), "./kcpsetup.go", nil, make(map[string]string), "root:openmfp-system", &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error")).Once()
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-openmfp-system.yaml", cl, make(map[string]string), "root:openmfp-system", &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-orgs.yaml", cl, make(map[string]string), "root:orgs", &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	templateData := map[string]string{
		".account-operator.webhooks.platform-mesh.io.ca-bundle": "CABundle",
	}
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/03-openmfp-system/mutatingwebhookconfiguration-admissionregistration.k8s.io.yaml", cl, templateData, "root:openmfp-system", &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)
}

func (s *KcpsetupTestSuite) TestCreateWorkspaces() {
	// Mock the CA secret lookup - expect it twice since it's called in both getCABundleInventory and createKcpResources
	webhookConfig := subroutines.DEFAULT_WEBHOOK_CONFIGURATION
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      webhookConfig.SecretRef.Name,
		Namespace: webhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				webhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Times(2)

	// Mock the second secret lookup
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      "account-operator-webhook-server-cert",
		Namespace: "openmfp-system",
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				"ca.crt": []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	// test err1 - expect error when NewKcpClient fails
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(nil, errors.New("failed to create client"))
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, ManifestStructureTest, "")

	err := s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to get APIExport hash inventory")

	// test OK
	mockedK8sClient := new(mocks.Client)
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper = new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj = subroutines.NewKcpsetupSubroutine(mockedK8sClient, mockedKcpHelper, ManifestStructureTest, "")

	// Mock both secret lookups
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      webhookConfig.SecretRef.Name,
		Namespace: webhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				webhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Times(2)

	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      "account-operator-webhook-server-cert",
		Namespace: "openmfp-system",
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				"ca.crt": []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	apiexport := &kcpapiv1alpha.APIExport{
		Status: kcpapiv1alpha.APIExportStatus{
			IdentityHash: "hash1",
		},
	}
	workspace := &kcptenancyv1alpha.Workspace{
		Status: kcptenancyv1alpha.WorkspaceStatus{
			Phase: "Ready",
		},
	}
	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			_, ok := o.(*kcpapiv1alpha.APIExport)
			if ok {
				*o.(*kcpapiv1alpha.APIExport) = *apiexport
			}
			_, ok = o.(*kcptenancyv1alpha.Workspace)
			if ok {
				*o.(*kcptenancyv1alpha.Workspace) = *workspace
			}

			return nil
		})
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)

	// test err2 - expect error when Patch fails
	mockKcpClient = new(mocks.Client)
	mockedKcpHelper = new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj = subroutines.NewKcpsetupSubroutine(mockedK8sClient, mockedKcpHelper, ManifestStructureTest, "")

	// Mock both secret lookups again
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      webhookConfig.SecretRef.Name,
		Namespace: webhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				webhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Times(2)

	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      "account-operator-webhook-server-cert",
		Namespace: "openmfp-system",
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				"ca.crt": []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			_, ok := o.(*kcpapiv1alpha.APIExport)
			if ok {
				*o.(*kcpapiv1alpha.APIExport) = *apiexport
			}
			_, ok = o.(*kcptenancyv1alpha.Workspace)
			if ok {
				*o.(*kcptenancyv1alpha.Workspace) = *workspace
			}
			return nil
		})
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch failed"))
	err = s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to apply manifest file")
}

func (s *KcpsetupTestSuite) TestUnstructuredFromFile() {

	path := "../../manifests/kcp/01-openmfp-system/contentconfiguration-main-iam-ui.yaml"
	templateData := map[string]string{
		"baseDomain": "example1.com",
	}
	logcfg := logger.DefaultConfig()
	// logcfg.Level = defaultCfg.Log.Level
	// logcfg.NoJSON = defaultCfg.Log.NoJson
	var err error
	log, err := logger.New(logcfg)
	if err != nil {
		panic(err)
	}

	obj, err := s.testObj.UnstructuredFromFile(path, templateData, log)
	s.Assert().Nil(err)
	s.Assert().Equal(obj.GetKind(), "ContentConfiguration")
	spec := obj.Object["spec"].(map[string]interface{})
	content := spec["inlineConfiguration"].(map[string]interface{})
	contentJSON, err := json.Marshal(content)
	s.Assert().Nil(err)
	s.Assert().Truef(strings.Contains(string(contentJSON), "{{members}}"), "Content does not contain expected URL")

}
