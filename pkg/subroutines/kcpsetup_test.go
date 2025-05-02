package subroutines_test

import (
	"context"
	"errors"
	"testing"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/context/keys"
	"github.com/openmfp/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	"github.com/openmfp/openmfp-operator/pkg/subroutines/mocks"
)

var ManifestStructureTest = subroutines.DirectoryStructure{
	Workspaces: []subroutines.WorkspaceDirectory{
		{
			Name: "root",
			Files: []string{
				"../../setup/workspace-openmfp-system.yaml",
				"../../setup/workspace-type-provider.yaml",
				"../../setup/workspace-type-providers.yaml",
				"../../setup/workspace-type-org.yaml",
				"../../setup/workspace-type-orgs.yaml",
				"../../setup/workspace-type-account.yaml",
				"../../setup/workspace-orgs.yaml",
			},
		},
		{
			Name: "root:openmfp-system",
			Files: []string{
				"../../setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiexport-fga.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiexport-kcp.io.yaml",
				"../../setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiexportendpointslice-fga.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-accountinfos.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-accounts.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-authorizationmodels.core.openmfp.org.yaml",
				"../../setup/01-openmfp-system/apiresourceschema-stores.core.openmfp.org.yaml",
			},
		},
		{
			Name: "root:orgs",
			Files: []string{
				"../../setup/02-orgs/account-root-org.yaml",
				"../../setup/02-orgs/workspace-root-org.yaml",
			},
		},
	},
}

type KcpsetupTestSuite struct {
	suite.Suite

	testObj *subroutines.KcpsetupSubroutine

	// mocks
	clientMock *mocks.Client

	log *logger.Logger
}

func TestKcpsetupTestSuite(t *testing.T) {
	suite.Run(t, new(KcpsetupTestSuite))
}

func (s *KcpsetupTestSuite) Test_getCABundleInventory() {
	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	expectedCaData := []byte("test-ca-data")

	// Test case 1: No webhook configurations, use default
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Name: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name, Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace}, mock.AnythingOfType("*v1.Secret")).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
			secret := obj.(*corev1.Secret)
			secret.Data = map[string][]byte{
				subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretData: expectedCaData,
			}
			return nil
		}).Once()

	inventory, err := s.testObj.GetCABundleInventory(ctx)
	s.Assert().NoError(err)
	s.Assert().NotNil(inventory)
	expectedKey := subroutines.DEFAULT_WEBHOOK_CONFIGURATION.WebhookRef.Name + ".ca-bundle"
	s.Assert().Contains(inventory, expectedKey)
	s.Assert().Equal(string(expectedCaData), inventory[expectedKey])
	s.clientMock.AssertExpectations(s.T())

	// Test case 2: No webhook configurations, getCaBundle fails
	s.clientMock.EXPECT().Get(mock.Anything, types.NamespacedName{Name: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Name, Namespace: subroutines.DEFAULT_WEBHOOK_CONFIGURATION.SecretRef.Namespace}, mock.AnythingOfType("*v1.Secret")).
		Return(errors.New("secret not found")).Once()

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

func (suite *KcpsetupTestSuite) SetupTest() {
	// create new logger
	suite.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	suite.clientMock = new(mocks.Client)

	// create new test object
	suite.testObj = subroutines.NewKcpsetupSubroutine(suite.clientMock, &subroutines.Helper{}, ManifestStructureTest, "wave1")
}

func (suite *KcpsetupTestSuite) TearDownTest() {
	// clear mocks
	suite.clientMock = nil

	// clear test object
	suite.testObj = nil
}

func (s *KcpsetupTestSuite) TestProcess() {
	instance := &corev1alpha1.OpenMFP{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OpenMFP",
			APIVersion: "openmfp.core.openmfp.org/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: corev1alpha1.OpenMFPSpec{
			Kcp: corev1alpha1.Kcp{
				AdminSecretRef: &corev1alpha1.AdminSecretRef{
					SecretRef: corev1alpha1.SecretReference{
						Name:      "test-secret",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
				ProviderConnections: []corev1alpha1.ProviderConnection{
					{
						EndpointSliceName: "test-endpoint",
						Path:              "root:openmfp-system",
						Secret:            "test-secret",
					},
				},
			},
		},
		Status: corev1alpha1.OpenMFPStatus{
			KcpWorkspaces: []corev1alpha1.KcpWorkspace{
				{
					Name:  "root:openmfp-system",
					Phase: "Ready",
				},
				{
					Name:  "root:orgs",
					Phase: "Ready",
				},
			},
		},
	}

	// mocks
	s.clientMock.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	scheme := runtime.NewScheme()
	err := corev1alpha1.AddToScheme(scheme)
	s.Assert().NoError(err)
	s.clientMock.EXPECT().Scheme().Return(scheme).Once()

	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, &corev1.Secret{}).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"kubeconfig": secretKubeconfigData,
				},
			}
			return nil
		},
	).Once()
	s.clientMock.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"ca.crt": []byte("CABundle"),
				},
			}
			return nil
		}).Once()

	apiexport := &kcpapiv1alpha.APIExport{
		Status: kcpapiv1alpha.APIExportStatus{
			IdentityHash: "hash1",
		},
	}

	mockKcpClient := new(mocks.Client)
	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return nil
		}).Times(3)

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()

	workspace := kcptenancyv1alpha.Workspace{
		Status: kcptenancyv1alpha.WorkspaceStatus{
			Phase: "Ready",
		},
	}
	mockKcpClient2 := new(mocks.Client)
	mockKcpClient2.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockKcpClient2.EXPECT().Get(
		mock.Anything, types.NamespacedName{Name: "openmfp-system"}, mock.Anything,
	).RunAndReturn(func(
		ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*kcptenancyv1alpha.Workspace) = workspace
		return nil
	}).Once()
	mockKcpClient2.EXPECT().Get(
		mock.Anything, types.NamespacedName{Name: "orgs"}, mock.Anything,
	).RunAndReturn(func(
		ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*kcptenancyv1alpha.Workspace) = workspace
		return nil
	}).Once()
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient2, nil)

	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, ManifestStructureTest, "wave1")

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)

	// assert
	s.Nil(opErr)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *KcpsetupTestSuite) Test_getAPIExportHashInventory() {
	// mocks
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Times(3)
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, mockedKcpHelper, ManifestStructureTest, "wave1")

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
	s.testObj = subroutines.NewKcpsetupSubroutine(s.clientMock, helper, ManifestStructureTest, "wave1")
}

func (s *KcpsetupTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers()
	s.Assert().Equal(res, []string{subroutines.ProvidersecretSubroutineFinalizer})
}

func (s *KcpsetupTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.KcpsetupSubroutineName+".wave1")
}

func (s *KcpsetupTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *KcpsetupTestSuite) TestApplyManifestFromFile() {

	client := new(mocks.Client)
	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err := s.testObj.ApplyManifestFromFile(context.TODO(), "../../setup/workspace-openmfp-system.yaml", client, make(map[string]string))
	s.Assert().Nil(err)

	err = s.testObj.ApplyManifestFromFile(context.TODO(), "invalid", nil, make(map[string]string))
	s.Assert().Error(err)

	err = s.testObj.ApplyManifestFromFile(context.TODO(), "./kcpsetup.go", nil, make(map[string]string))
	s.Assert().Error(err)

	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error")).Once()
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../setup/workspace-openmfp-system.yaml", client, make(map[string]string))
	s.Assert().Error(err)

	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../setup/workspace-orgs.yaml", client, make(map[string]string))
	s.Assert().Nil(err)

	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	templateData := map[string]string{
		".account-operator.webhooks.core.openmfp.org.ca-bundle": "CABundle",
	}
	err = s.testObj.ApplyManifestFromFile(context.TODO(), "../../setup/01-openmfp-system/mutatingwebhookconfiguration-admissionregistration.k8s.io.yaml", client, templateData)
	s.Assert().Nil(err)
}

func (s *KcpsetupTestSuite) TestCreateWorkspaces() {

	// test err1
	err := s.testObj.CreateKcpResources(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	},
		"kubeconfig",
		ManifestStructureTest,
		&corev1alpha1.OpenMFP{},
	)
	s.Assert().Error(err)

	// test OK
	mockedK8sClient := new(mocks.Client)
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj = subroutines.NewKcpsetupSubroutine(mockedK8sClient, mockedKcpHelper, ManifestStructureTest, "wave1")

	mockedK8sClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = corev1.Secret{
				Data: map[string][]byte{
					"ca.crt": []byte("CABundle"),
				},
			}
			return nil
		}).Once()

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
	err = s.testObj.CreateKcpResources(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}, "kubeconfig", ManifestStructureTest, &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)

	// test err2
	err = s.testObj.CreateKcpResources(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": []byte("invaliddata"),
		},
	}, "kubeconfig", ManifestStructureTest, &corev1alpha1.OpenMFP{})
	s.Assert().Error(err)
}
