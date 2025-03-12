package subroutines

import (
	"errors"
	"testing"

	"context"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	kcptenancyv1alpha "github.com/kcp-dev/kcp/sdk/apis/tenancy/v1alpha1"
	"github.com/openmfp/golang-commons/context/keys"
	"github.com/openmfp/golang-commons/logger"
	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KcpsetupTestSuite struct {
	suite.Suite

	testObj *KcpsetupSubroutine

	// mocks
	clientMock *mocks.Client

	log *logger.Logger
}

func TestKcpsetupTestSuite(t *testing.T) {
	suite.Run(t, new(KcpsetupTestSuite))
}

func (suite *KcpsetupTestSuite) SetupTest() {
	// create new logger
	suite.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	suite.clientMock = new(mocks.Client)

	// create new test object
	suite.testObj = NewKcpsetupSubroutine(suite.clientMock, nil)
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
				AdminSecretRef: corev1alpha1.AdminSecretRef{
					Name: "test-secret",
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
	mockedKcpHelper.EXPECT().GetSecret(mock.Anything, mock.Anything, mock.Anything).
		Return(&corev1.Secret{
			Data: map[string][]byte{
				"kubeconfig": secretKubeconfigData,
			},
		}, nil).Once()

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

	s.testObj.kcpHelper = mockedKcpHelper

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
	s.testObj.kcpHelper = mockedKcpHelper

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

	inventory, err := s.testObj.getAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(APIExportInventory{
		ApiExportRootTenancyKcpIoIdentityHash: "hash1",
		ApiExportRootShardsKcpIoIdentityHash:  "hash1",
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

	inventory, err = s.testObj.getAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(APIExportInventory{
		ApiExportRootTenancyKcpIoIdentityHash: "hash1",
	}, inventory)

	// test error 3
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExport) = *apiexport
			return errors.New("error")
		}).Once()

	inventory, err = s.testObj.getAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(APIExportInventory{
		ApiExportRootTenancyKcpIoIdentityHash: "",
	}, inventory)

	// test error 4
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(nil, errors.New("Error")).Once()
	inventory, err = s.testObj.getAPIExportHashInventory(context.TODO(), &rest.Config{})
	s.Assert().Error(err)
	s.Assert().Equal(APIExportInventory{
		ApiExportRootTenancyKcpIoIdentityHash: "",
	}, inventory)
}

func (s *KcpsetupTestSuite) Test_Constructor() {
	// create new logger
	s.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	s.clientMock = new(mocks.Client)
	helper := &Helper{}

	// create new test object
	s.testObj = NewKcpsetupSubroutine(s.clientMock, helper)
}

func (s *KcpsetupTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers()
	s.Assert().Equal(res, []string{ProvidersecretSubroutineFinalizer})
}

func (s *KcpsetupTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, KcpsetupSubroutineName)
}

func (s *KcpsetupTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *KcpsetupTestSuite) TestApplyManifestFromFile() {

	client := new(mocks.Client)
	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err := s.testObj.applyManifestFromFile(context.TODO(), "../../test/setup/workspace-openmfp-system.yaml", client, APIExportInventory{})
	s.Assert().Nil(err)

	err = s.testObj.applyManifestFromFile(context.TODO(), "invalid", nil, APIExportInventory{})
	s.Assert().Error(err)

	err = s.testObj.applyManifestFromFile(context.TODO(), "./kcpsetup.go", nil, APIExportInventory{})
	s.Assert().Error(err)

	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error")).Once()
	err = s.testObj.applyManifestFromFile(context.TODO(), "../../test/setup/workspace-openmfp-system.yaml", client, APIExportInventory{})
	s.Assert().Error(err)

	client.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err = s.testObj.applyManifestFromFile(context.TODO(), "../../test/setup/workspace-orgs.yaml", client, APIExportInventory{})
	s.Assert().Nil(err)

}

func (s *KcpsetupTestSuite) TestCreateWorkspaces() {

	// test err1
	err := s.testObj.createKcpWorkspaces(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	})
	s.Assert().Error(err)

	// test OK
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)
	s.testObj.kcpHelper = mockedKcpHelper

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
	err = s.testObj.createKcpWorkspaces(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	})
	s.Assert().Nil(err)

	// test err2
	err = s.testObj.createKcpWorkspaces(context.Background(), corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": []byte("invaliddata"),
		},
	})
	s.Assert().Error(err)
}
