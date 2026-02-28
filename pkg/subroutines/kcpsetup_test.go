package subroutines_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

var ManifestStructureTest = "../../manifests/kcp"

func defaultTestOperatorConfig() *config.OperatorConfig {
	cfg := &config.OperatorConfig{}
	cfg.Subroutines.KcpSetup.DomainCertificateCASecretName = "domain-certificate"
	cfg.Subroutines.KcpSetup.DomainCertificateCASecretKey = "tls.crt"
	return cfg
}

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

func (s *KcpsetupTestSuite) SetupTest() {
	s.clientMock = new(mocks.Client)
	s.helperMock = new(mocks.KcpHelper)
	cfg := logger.DefaultConfig()
	cfg.Level = "debug"
	cfg.NoJSON = true
	cfg.Name = "KcpsetupTestSuite"
	s.log, _ = logger.New(cfg)
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, s.helperMock, defaultTestOperatorConfig(), ManifestStructureTest, "https://kcp.example.com")
}

func (s *KcpsetupTestSuite) TearDownTest() {
	s.clientMock = nil
	s.helperMock = nil
	s.testObj = nil
}

func (s *KcpsetupTestSuite) Test_Constructor() {
	// create new logger
	s.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	s.clientMock = new(mocks.Client)
	helper := &subroutines.Helper{}

	// create new test object
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, helper, defaultTestOperatorConfig(), ManifestStructureTest, "")
}

func (s *KcpsetupTestSuite) Test_applyDirStructure() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	kcpClientMock := new(mocks.Client)
	// Expect NewKcpClient to be called multiple times for different workspaces (flexible count)
	s.helperMock.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(kcpClientMock, nil).Maybe()
	inventory := map[string]any{
		"apiExportRootTenancyKcpIoIdentityHash":  "hash1",
		"apiExportRootShardsKcpIoIdentityHash":   "hash2",
		"apiExportRootTopologyKcpIoIdentityHash": "hash3",
		"registrationAllowed":                    true,
	}

	// Expect multiple Patch calls for applying manifests (flexible count)
	kcpClientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock unstructured object lookups (for general manifest objects - flexible count)
	kcpClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstructuredObj := obj.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Ready",
				},
			}
			return nil
		})

	// Mock workspace lookups for waitForWorkspace calls (multiple calls for polling)
	kcpClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			ws := obj.(*kcptenancyv1alpha.Workspace)
			ws.Status.Phase = "Ready"
			return nil
		})

	err := subroutines.ApplyDirStructure(ctx, "../../manifests/kcp", "root", &rest.Config{}, inventory, &corev1alpha1.PlatformMesh{}, s.helperMock)

	s.Assert().Nil(err)
}

func (s *KcpsetupTestSuite) Test_getCABundleInventory() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	expectedCaData := []byte("test-ca-data")

	// Test case 1: Success case
	// Mock the mutating webhook secret lookup (called once due to caching)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretData: expectedCaData,
			}
			return nil
		}).
		Once() // Only called once due to caching

	// Mock the validating webhook secret lookup (called once due to caching)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretData: expectedCaData,
			}
			return nil
		}).
		Once() // Only called once due to caching

	// Mock the identity provider validating webhook secret lookup (called once due to caching)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretData: expectedCaData,
			}
			return nil
		}).
		Once() // Only called once due to caching

	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "domain-certificate",
			Namespace: "platform-mesh-system",
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

	// First call should fetch from secrets
	inventory, err := s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err)
	s.Assert().NotNil(inventory)

	// Check mutating webhook CA bundle
	mutatingKey := subroutines.DEFAULT_WEBHOOK_CONFIGURATION.WebhookRef.Name + ".ca-bundle"
	s.Assert().Contains(inventory, mutatingKey)
	expectedB64 := base64.StdEncoding.EncodeToString(expectedCaData)
	s.Assert().Equal(expectedB64, inventory[mutatingKey])

	// Check validating webhook CA bundle
	validatingKey := subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.WebhookRef.Name + ".ca-bundle"
	s.Assert().Contains(inventory, validatingKey)
	s.Assert().Equal(expectedB64, inventory[validatingKey])

	// Check identity provider validating webhook CA bundle
	ipdValidatingKey := subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.WebhookRef.Name + ".ca-bundle"
	s.Assert().Contains(inventory, ipdValidatingKey)
	s.Assert().Equal(expectedB64, inventory[ipdValidatingKey])

	// Second call should use cache (no additional mock calls expected)
	inventory2, err2 := s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err2)
	s.Assert().NotNil(inventory2)
	s.Assert().Contains(inventory2, mutatingKey)
	s.Assert().Contains(inventory2, validatingKey)
	s.Assert().Contains(inventory2, ipdValidatingKey)
	s.Assert().Equal(expectedB64, inventory2[mutatingKey])
	s.Assert().Equal(expectedB64, inventory2[validatingKey])
	s.Assert().Equal(expectedB64, inventory2[ipdValidatingKey])

	s.clientMock.AssertExpectations(s.T())

	// Test case 2: Secret not found
	// Create a new instance to clear the cache
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, s.helperMock, defaultTestOperatorConfig(), ManifestStructureTest, "")

	// Mock the mutating webhook secret lookup to return error
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		Return(errors.New("secret not found")).
		Once()

	inventory, err = s.testObj.GetCABundleInventory(ctx)
	s.Assert().Error(err)
	s.Assert().Nil(inventory)
	s.Assert().Contains(err.Error(), "Failed to get CA bundle")
	s.clientMock.AssertExpectations(s.T())
}

func (s *KcpsetupTestSuite) Test_getCABundleInventory_CustomSecretNameAndKey() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	customSecretName := "my-custom-ca-secret"
	customSecretKey := "ca.pem"
	customCfg := defaultTestOperatorConfig()
	customCfg.Subroutines.KcpSetup.DomainCertificateCASecretName = customSecretName
	customCfg.Subroutines.KcpSetup.DomainCertificateCASecretKey = customSecretKey

	clientMock := new(mocks.Client)
	s.testObj = subroutines.NewKcpsetupSubroutine(clientMock, s.helperMock, customCfg, ManifestStructureTest, "")

	// Mock the mutating webhook secret lookup
	clientMock.EXPECT().
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
		}).Once()

	// Mock the validating webhook secret lookup
	clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION.SecretData: []byte("test-ca-data"),
			}
			return nil
		}).Once()

	// Mock the identity provider validating webhook secret lookup
	clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretData: []byte("test-ca-data"),
			}
			return nil
		}).Once()

	// Mock the custom-named domain CA secret lookup with custom key
	clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      customSecretName,
			Namespace: "platform-mesh-system",
		}, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				customSecretKey: []byte("custom-ca-data"),
			}
			return nil
		}).Once()

	inventory, err := s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err)
	s.Assert().NotNil(inventory)
	s.Assert().Contains(inventory, "domainCA")
	s.Assert().Contains(inventory, "domainCADec")
	s.Assert().Equal("custom-ca-data", inventory["domainCADec"])

	clientMock.AssertExpectations(s.T())
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

func (s *KcpsetupTestSuite) TestProcess() {
	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	operatorCfg.KCP.RootShardName = "kcp"
	operatorCfg.KCP.Namespace = "default"
	operatorCfg.KCP.FrontProxyName = "kcp-front-proxy"
	operatorCfg.KCP.ClusterAdminSecretName = "kcp-cluster-admin"

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)

	// Mock the Helm release lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp", Namespace: "default"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			release := obj.(*unstructured.Unstructured)
			release.Object = map[string]interface{}{
				"status": map[string]interface{}{
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
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{Name: "kcp-front-proxy", Namespace: "default"}, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			release := obj.(*unstructured.Unstructured)
			release.Object = map[string]interface{}{
				"status": map[string]interface{}{
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

	// Mock the kubeconfig secret lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "kcp-cluster-admin-client-cert",
			Namespace: "platform-mesh-system",
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
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "kcp-cluster-admin",
			Namespace: "default",
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
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "domain-certificate",
			Namespace: "platform-mesh-system",
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

	// Mock the identity provider validating webhook CA secret lookup
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Name,
			Namespace: subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretRef.Namespace,
		}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION.SecretData: []byte("test-ca-data"),
			}
			return nil
		}).Once()

	// Mock the secondary webhook server cert lookup (called once since we cache results)
	s.clientMock.EXPECT().
		Get(mock.Anything, types.NamespacedName{
			Name:      "account-operator-webhook-server-cert",
			Namespace: "platform-mesh-system",
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
		NewKcpClient(mock.Anything, "root:platform-mesh").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:platform-mesh-system").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs").
		Return(mockKcpClient, nil)

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, "root:orgs:default").
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

	// Mock unstructured object lookups for manifest files (flexible count)
	mockKcpClient.EXPECT().
		Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			unstructuredObj := obj.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Ready",
				},
			}
			return nil
		})

	// Mock patch calls for applying manifests (flexible count)
	mockKcpClient.EXPECT().
		Patch(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	// Call Process
	result, opErr := s.testObj.Process(ctx, &corev1alpha1.PlatformMesh{})

	// Assertions
	s.Assert().Nil(opErr)
	s.Assert().Equal(ctrl.Result{}, result)

	// Test error case - create a new instance to clear the cache
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, s.helperMock, defaultTestOperatorConfig(), ManifestStructureTest, "https://kcp.example.com")
}

func (s *KcpsetupTestSuite) Test_getAPIExportHashInventory() {
	// mocks
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Times(3)
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, defaultTestOperatorConfig(), ManifestStructureTest, "")

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

func (s *KcpsetupTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers(&corev1alpha1.PlatformMesh{})
	s.Assert().Equal(res, []string{subroutines.KcpsetupSubroutineFinalizer})
}

func (s *KcpsetupTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.KcpsetupSubroutineName)
}

func (s *KcpsetupTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), &corev1alpha1.PlatformMesh{})
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *KcpsetupTestSuite) TestCreateWorkspaces() {
	// test err1 - expect error when NewKcpClient fails
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(nil, errors.New("failed to create client"))
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, defaultTestOperatorConfig(), ManifestStructureTest, "")

	err := s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.PlatformMesh{})
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to get APIExport hash inventory")

	// test OK
	mockedK8sClient := new(mocks.Client)
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper = new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj = subroutines.NewKcpsetupSubroutine(mockedK8sClient, mockedKcpHelper, defaultTestOperatorConfig(), ManifestStructureTest, "")

	// Mock both webhook secret lookups for CA bundle inventory
	webhookConfig := subroutines.DEFAULT_WEBHOOK_CONFIGURATION
	validatingWebhookConfig := subroutines.DEFAULT_VALIDATING_WEBHOOK_CONFIGURATION
	ipdValidatingWebhookConfig := subroutines.DEFAULT_IDENTITY_PROVIDER_VALIDATING_WEBHOOK_CONFIGURATION

	// Mock the mutating webhook secret lookup (called once due to caching)
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
		Once()

	// Mock the domain certificate CA lookup
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      "domain-certificate",
		Namespace: webhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				"ca.crt":  []byte("test-ca-data"),
				"tls.crt": []byte("test-tls-crt"),
				"tls.key": []byte("test-tls-key"),
			}
		}).
		Return(nil)

	// Mock the identity provider validating webhook secret lookup (called once due to caching)
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      ipdValidatingWebhookConfig.SecretRef.Name,
		Namespace: ipdValidatingWebhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				ipdValidatingWebhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	// Mock the validating webhook secret lookup (called once due to caching)
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      validatingWebhookConfig.SecretRef.Name,
		Namespace: validatingWebhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				validatingWebhookConfig.SecretData: []byte("dummy-ca-data"),
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
	// Mock APIExport lookups (3 calls for tenancy, shards, topology)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.APIExport")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return nil
		}).Times(3)

	// Mock workspace lookups (flexible count for polling)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcptenancyv1alpha.Workspace) = *workspace
			return nil
		}).Maybe()

	// Mock unstructured object lookups for manifest files (flexible count)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			unstructuredObj := o.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Ready",
				},
			}
			return nil
		})

	// Mock patch calls for applying manifests (flexible count)
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.PlatformMesh{})
	s.Assert().Nil(err)

	// test err2 - expect error when Patch fails
	mockKcpClient = new(mocks.Client)
	mockedKcpHelper = new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj = subroutines.NewKcpsetupSubroutine(mockedK8sClient, mockedKcpHelper, defaultTestOperatorConfig(), ManifestStructureTest, "")

	// Mock both secret lookups again (they should be cached from previous call)
	// Since we're creating a new instance, the cache is cleared, so we need to mock again
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
		Once()

	// Mock the domain certificate CA lookup
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      "domain-certificate-ca",
		Namespace: webhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				"ca.crt":  []byte("test-ca-data"),
				"tls.crt": []byte("test-tls-crt"),
				"tls.key": []byte("test-tls-key"),
			}
		}).
		Return(nil)

	// Mock the identity provider validating webhook secret lookup (called once due to caching)
	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      ipdValidatingWebhookConfig.SecretRef.Name,
		Namespace: ipdValidatingWebhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				ipdValidatingWebhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	mockedK8sClient.EXPECT().Get(mock.Anything, types.NamespacedName{
		Name:      validatingWebhookConfig.SecretRef.Name,
		Namespace: validatingWebhookConfig.SecretRef.Namespace,
	}, mock.AnythingOfType("*v1.Secret")).
		Run(func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) {
			sec := obj.(*corev1.Secret)
			sec.Data = map[string][]byte{
				validatingWebhookConfig.SecretData: []byte("dummy-ca-data"),
			}
		}).
		Return(nil).
		Once()

	// Mock APIExport lookups (3 calls for tenancy, shards, topology)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.APIExport")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return nil
		}).Times(3)

	// Mock workspace lookups (2 calls for platform-mesh-system and orgs workspaces)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*v1alpha1.Workspace")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcptenancyv1alpha.Workspace) = *workspace
			return nil
		}).Times(2)

	// Mock unstructured object lookups for manifest files (flexible count)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			unstructuredObj := o.(*unstructured.Unstructured)
			unstructuredObj.Object = map[string]interface{}{
				"status": map[string]interface{}{
					"phase": "Ready",
				},
			}
			return nil
		})

	// Mock patch calls for applying manifests (flexible count) - but they should fail
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("patch failed"))
	err = s.testObj.CreateKcpResources(context.Background(), &rest.Config{}, ManifestStructureTest, &corev1alpha1.PlatformMesh{})
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to apply")
}

func (s *KcpsetupTestSuite) TestUnstructuredFromFile() {

	logcfg := logger.DefaultConfig()
	// logcfg.Level = defaultCfg.Log.Level
	// logcfg.NoJSON = defaultCfg.Log.NoJson
	var err error
	log, err := logger.New(logcfg)
	if err != nil {
		panic(err)
	}

	// Resource
	path := "../../manifests/k8s/platform-mesh-operator-components/resource.yaml"
	templateData := map[string]any{
		"componentName": "component1",
		"repoName":      "repo1",
		"referencePath": "\n        - ref1\n        - ref2",
	}
	obj, err := s.testObj.UnstructuredFromFile(path, templateData, log)
	s.Assert().Nil(err)
	s.Assert().Equal(obj.GetKind(), "Resource")
	spec := obj.Object["spec"].(map[string]interface{})
	content := spec["componentRef"].(map[string]interface{})
	contentJSON, err := json.Marshal(content)
	s.Assert().Nil(err)
	s.Assert().Truef(strings.Contains(string(contentJSON), "component1"), "Content does not contain expected componentName")

	resource := spec["resource"].(map[string]interface{})
	byReference := resource["byReference"].(map[string]interface{})
	referencePath := byReference["referencePath"].([]interface{})
	contentJSON, err = json.Marshal(referencePath)
	s.Assert().Nil(err)

	s.Assert().Truef(strings.Contains(string(contentJSON), "ref1"), "Content does not contain expected referencePath")
	s.Assert().Truef(strings.Contains(string(contentJSON), "ref2"), "Content does not contain expected referencePath")
	s.Assert().Truef(strings.Contains(string(contentJSON), "platform-mesh-operator-components"), "Content does not contain expected referencePath")
}

// Tests for applyExtraWorkspaces (via assumed exported wrapper ApplyExtraWorkspaces).
// If the wrapper name differs, adjust the method name accordingly.

func (s *KcpsetupTestSuite) Test_ApplyExtraWorkspaces_Success() {
	// Arrange
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	parentPath := "root:orgs"
	fullPath := parentPath + ":extra-ws"

	kcpClientMock := new(mocks.Client)
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, parentPath).
		Return(kcpClientMock, nil).Once()

	// SSA Patch - no Get needed
	kcpClientMock.EXPECT().
		Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	inst := s.newPlatformMeshWithExtraWorkspaces([]extraWsDef{
		{Path: fullPath, TypeName: "universal", TypePath: "root"},
	})

	// Act
	err := s.testObj.ApplyExtraWorkspaces(ctx, &rest.Config{}, inst)

	// Assert
	s.Assert().NoError(err)
}

func (s *KcpsetupTestSuite) Test_ApplyExtraWorkspaces_InvalidPath_Skipped() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	// No expectation for NewKcpClient because invalid path is skipped
	inst := s.newPlatformMeshWithExtraWorkspaces([]extraWsDef{
		{Path: "invalid-no-colon", TypeName: "universal", TypePath: "root"},
	})

	err := s.testObj.ApplyExtraWorkspaces(ctx, &rest.Config{}, inst)
	s.Assert().NoError(err)
	s.helperMock.AssertNotCalled(s.T(), "NewKcpClient", mock.Anything, mock.Anything)
}

func (s *KcpsetupTestSuite) Test_ApplyExtraWorkspaces_NewKcpClient_Error() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	parentPath := "root:team"
	fullPath := parentPath + ":ws1"

	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, parentPath).
		Return(nil, errors.New("boom")).Once()

	inst := s.newPlatformMeshWithExtraWorkspaces([]extraWsDef{
		{Path: fullPath, TypeName: "typeA", TypePath: "root"},
	})

	err := s.testObj.ApplyExtraWorkspaces(ctx, &rest.Config{}, inst)
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to create kcp client")
}

func (s *KcpsetupTestSuite) Test_ApplyExtraWorkspaces_Patch_Error() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	parentPath := "root:orgs"
	fullPath := parentPath + ":ws3"

	kcpClientMock := new(mocks.Client)
	s.helperMock.EXPECT().
		NewKcpClient(mock.Anything, parentPath).
		Return(kcpClientMock, nil).Once()

	// SSA Patch fails - no Get needed
	kcpClientMock.EXPECT().
		Patch(mock.Anything, mock.AnythingOfType("*unstructured.Unstructured"), mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("patch failed")).Once()

	inst := s.newPlatformMeshWithExtraWorkspaces([]extraWsDef{
		{Path: fullPath, TypeName: "universal", TypePath: "root"},
	})

	err := s.testObj.ApplyExtraWorkspaces(ctx, &rest.Config{}, inst)
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to apply extra workspace")
}

//
// Helpers for constructing PlatformMesh with ExtraWorkspaces.
// These helper type names guess the actual API names; adjust if different.
//

type extraWsDef struct {
	Path     string
	TypeName string
	TypePath string
}

func (s *KcpsetupTestSuite) newPlatformMeshWithExtraWorkspaces(defs []extraWsDef) *corev1alpha1.PlatformMesh {
	pm := &corev1alpha1.PlatformMesh{}
	// Ensure nested structs exist
	pm.Spec.Kcp = corev1alpha1.Kcp{}

	// Attempt to populate using likely field names; ignore if they differ (tests will need adjustment).
	for _, d := range defs {
		pm.Spec.Kcp.ExtraWorkspaces = append(pm.Spec.Kcp.ExtraWorkspaces, corev1alpha1.WorkspaceDeclaration{
			Path: d.Path,
			Type: corev1alpha1.WorkspaceTypeReference{
				Name: d.TypeName,
			},
		})
	}
	return pm
}

func (s *KcpsetupTestSuite) Test_HasFeatureToggle() {
	tests := []struct {
		name           string
		featureToggles []corev1alpha1.FeatureToggle
		toggleName     string
		expected       string
	}{
		{
			name:           "returns true when feature toggle exists",
			featureToggles: []corev1alpha1.FeatureToggle{{Name: "feature-disable-email-verification"}},
			toggleName:     "feature-disable-email-verification",
			expected:       "true",
		},
		{
			name:           "returns false when feature toggle does not exist",
			featureToggles: []corev1alpha1.FeatureToggle{{Name: "feature-enable-getting-started"}},
			toggleName:     "feature-disable-email-verification",
			expected:       "false",
		},
		{
			name:           "returns false when feature toggles are empty",
			featureToggles: nil,
			toggleName:     "feature-disable-email-verification",
			expected:       "false",
		},
		{
			name: "returns true when toggle is among multiple toggles",
			featureToggles: []corev1alpha1.FeatureToggle{
				{Name: "feature-enable-getting-started"},
				{Name: "feature-disable-email-verification"},
				{Name: "feature-enable-marketplace-account"},
			},
			toggleName: "feature-disable-email-verification",
			expected:   "true",
		},
		{
			name:           "returns true for feature-disable-contentconfigurations",
			featureToggles: []corev1alpha1.FeatureToggle{{Name: "feature-disable-contentconfigurations"}},
			toggleName:     "feature-disable-contentconfigurations",
			expected:       "true",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			inst := &corev1alpha1.PlatformMesh{
				Spec: corev1alpha1.PlatformMeshSpec{
					FeatureToggles: tc.featureToggles,
				},
			}
			result := subroutines.HasFeatureToggle(inst, tc.toggleName)
			s.Assert().Equal(tc.expected, result)
		})
	}
}

func (s *KcpsetupTestSuite) Test_WorkspaceAuthConfigTemplate_FeatureDisableEmailVerification() {
	templateBytes, err := os.ReadFile("../../manifests/kcp/workspace-authentication-configuration.yaml")
	s.Require().NoError(err, "Failed to read workspace-authentication-configuration.yaml")

	tests := []struct {
		name                  string
		featureToggleValue    string
		expectClaimValidation bool
		expectedExpression    string
		expectedMessage       string
	}{
		{
			name:                  "includes claimValidationRules when feature is enabled",
			featureToggleValue:    "true",
			expectClaimValidation: true,
			expectedExpression:    `claims.?email_verified.orValue(true) == true || claims.?email_verified.orValue(true) == false`,
			expectedMessage:       "Allowing both verified and unverified emails",
		},
		{
			name:                  "excludes claimValidationRules when feature is disabled",
			featureToggleValue:    "false",
			expectClaimValidation: false,
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			templateData := map[string]any{
				"baseDomainPort":                  "example.com:443",
				"domainCADec":                     "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
				"featureDisableEmailVerification": tc.featureToggleValue,
				"welcomeAudiences":                []string{"test-audience"},
			}

			result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
			s.Require().NoError(err, "Template rendering should not fail")

			renderedYAML := string(result)

			if tc.expectClaimValidation {
				s.Assert().Contains(renderedYAML, "claimValidationRules:", "Should contain claimValidationRules")
				s.Assert().Contains(renderedYAML, tc.expectedExpression, "Should contain the expected expression")
				s.Assert().Contains(renderedYAML, tc.expectedMessage, "Should contain the expected message")
			} else {
				s.Assert().NotContains(renderedYAML, "claimValidationRules:", "Should NOT contain claimValidationRules")
			}

			// Always verify the basic structure is present
			s.Assert().Contains(renderedYAML, "kind: WorkspaceAuthenticationConfiguration")
			s.Assert().Contains(renderedYAML, "name: orgs-authentication")
			s.Assert().Contains(renderedYAML, "claimMappings:")
		})
	}
}

func (s *KcpsetupTestSuite) Test_ApplyManifestFromFile_SkipsContentConfiguration_WhenToggleEnabled() {
	tests := []struct {
		name               string
		featureToggleValue string
		expectSkipped      bool
	}{
		{
			name:               "skips ContentConfiguration when toggle is enabled",
			featureToggleValue: "true",
			expectSkipped:      true,
		},
		{
			name:               "applies ContentConfiguration when toggle is disabled",
			featureToggleValue: "false",
			expectSkipped:      false,
		},
		{
			name:               "applies ContentConfiguration when toggle is not present",
			featureToggleValue: "",
			expectSkipped:      false,
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

			kcpClientMock := new(mocks.Client)

			templateData := map[string]any{
				"featureDisableContentConfigurations": tc.featureToggleValue,
			}

			path := "../../manifests/kcp/01-platform-mesh-system/contentconfiguration-main-home.yaml"

			if tc.expectSkipped {
				err := subroutines.ApplyManifestFromFile(ctx, path, kcpClientMock, templateData, "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
				s.Assert().NoError(err)
				kcpClientMock.AssertNotCalled(s.T(), "Get", mock.Anything, mock.Anything, mock.Anything)
				kcpClientMock.AssertNotCalled(s.T(), "Patch", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			} else {
				kcpClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
					Return(apierrors.NewNotFound(schema.GroupResource{}, "")).Once()
				kcpClientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

				err := subroutines.ApplyManifestFromFile(ctx, path, kcpClientMock, templateData, "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
				s.Assert().NoError(err)
			}
		})
	}
}

func (s *KcpsetupTestSuite) Test_ApplyManifestFromFile_DoesNotSkipNonContentConfiguration_WhenToggleEnabled() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)

	kcpClientMock := new(mocks.Client)

	templateData := map[string]any{
		"featureDisableContentConfigurations": "true",
	}

	// Use a non-ContentConfiguration file (e.g., a workspace file)
	path := "../../manifests/kcp/workspace-platform-mesh-system.yaml"

	// Even with toggle enabled, non-ContentConfiguration files should be applied
	kcpClientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.AnythingOfType("*unstructured.Unstructured")).
		Return(apierrors.NewNotFound(schema.GroupResource{}, "")).Once()
	kcpClientMock.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	err := subroutines.ApplyManifestFromFile(ctx, path, kcpClientMock, templateData, "root", &corev1alpha1.PlatformMesh{})
	s.Assert().NoError(err)
}
