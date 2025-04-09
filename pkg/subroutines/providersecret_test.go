package subroutines_test

import (
	"context"
	"errors"
	"os"
	"testing"

	kcpapiv1alpha "github.com/kcp-dev/kcp/sdk/apis/apis/v1alpha1"
	"github.com/openmfp/golang-commons/context/keys"
	"github.com/openmfp/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	"github.com/openmfp/openmfp-operator/pkg/subroutines/mocks"
)

var secretKubeconfigData, _ = os.ReadFile("test/kubeconfig.yaml")

type ProvidersecretTestSuite struct {
	suite.Suite

	testObj *subroutines.ProvidersecretSubroutine

	// mocks
	clientMock *mocks.Client

	log *logger.Logger
}

func TestProvidersecretTestSuite(t *testing.T) {
	suite.Run(t, new(ProvidersecretTestSuite))
}

func (suite *ProvidersecretTestSuite) SetupTest() {
	// create new logger
	suite.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	suite.clientMock = new(mocks.Client)

	// create new test object
	suite.testObj = subroutines.NewProvidersecretSubroutine(suite.clientMock, nil)
}

func (suite *ProvidersecretTestSuite) TearDownTest() {
	// clear mocks
	suite.clientMock = nil

	// clear test object
	suite.testObj = nil
}

func (s *ProvidersecretTestSuite) TestProcess() {
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
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	s.clientMock.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = secret
			return nil
		}).Twice()
	s.clientMock.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	scheme := runtime.NewScheme()
	err := corev1alpha1.AddToScheme(scheme)
	s.Assert().NoError(err)
	s.clientMock.EXPECT().Scheme().Return(scheme).Once()

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mockKcpClient := new(mocks.Client)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()
	mockedKcpHelper.EXPECT().GetSecret(mock.Anything, mock.Anything, mock.Anything).
		Return(&corev1.Secret{
			Data: map[string][]byte{
				"kubeconfig": secretKubeconfigData,
			},
		}, nil).Once()

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProvidersecretSubroutine(s.clientMock, mockedKcpHelper)

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)

	// assert
	s.Nil(opErr)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestWrongScheme() {
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
	mocClient := new(mocks.Client)
	mocClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mocClient.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	s.Assert().NoError(err)
	mocClient.EXPECT().Scheme().Return(scheme).Once()

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mocClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).
		Return(mocClient, nil).Once()
	mockedKcpHelper.EXPECT().GetSecret(mock.Anything, mock.Anything, mock.Anything).
		Return(&corev1.Secret{
			Data: map[string][]byte{
				"kubeconfig": secretKubeconfigData,
			},
		}, nil).Once()

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProvidersecretSubroutine(mocClient, mockedKcpHelper)

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "unable to add corev1 to scheme")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestErrorCreatingSecret() {
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
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	mockClient := new(mocks.Client)
	mockClient.EXPECT().Create(
		mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error creating secret")).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = secret
		return errors.New("error getting secret")
	}).Once()

	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mockClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockClient, nil).Once()
	mockedKcpHelper.EXPECT().GetSecret(mock.Anything, mock.Anything, mock.Anything).
		Return(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"kubeconfig": secretKubeconfigData,
			},
		}, nil)

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProvidersecretSubroutine(mockClient, mockedKcpHelper)

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "error creating secret")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestFailedBuilidingKubeconfig() {
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
	slice := &kcpapiv1alpha.APIExportEndpointSlice{
		Status: kcpapiv1alpha.APIExportEndpointSliceStatus{
			APIExportEndpoints: []kcpapiv1alpha.APIExportEndpoint{
				{
					URL: "http://url",
				},
			},
		},
	}

	mockKcpClient := new(mocks.Client)
	mockKcpClient.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption) error {
			*o.(*kcpapiv1alpha.APIExportEndpointSlice) = *slice
			return nil
		}).Once()

	mockedKcpHelper := new(mocks.KcpHelper)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()
	mockedKcpHelper.EXPECT().GetSecret(mock.Anything, mock.Anything, mock.Anything).
		Return(&corev1.Secret{
			Data: map[string][]byte{
				"kubeconfig": []byte("invalid"),
			},
		}, nil).Once()

	// s.testObj.kcpHelper = mockedKcpHelper
	s.testObj = subroutines.NewProvidersecretSubroutine(mockKcpClient, mockedKcpHelper)

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "Failed to build config from kubeconfig string")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestErrorGettingSecret() {
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

	// mock client.Get
	secret := corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": []byte("invalid"),
		},
	}

	s.clientMock.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = secret
			return errors.New("error getting secret")
		}).Once()

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "Failed to build config from kubeconfig string")
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *ProvidersecretTestSuite) TestWorkspaceNotReady() {
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
					Phase: "NotReady",
				},
				{
					Name:  "root:orgs",
					Phase: "Ready",
				},
			},
		},
	}

	secret := corev1.Secret{
		Data: map[string][]byte{
			"kubeconfig": []byte("invalid"),
		},
	}
	s.clientMock.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
		) error {
			*o.(*corev1.Secret) = secret
			return errors.New("error getting secret")
		}).Once()

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, s.log)
	res, opErr := s.testObj.Process(ctx, instance)
	_ = opErr
	_ = res

	// assert
	s.Assert().Error(opErr.Err(), "Workspace root:openmfp-system is not ready")
}

func (s *ProvidersecretTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers()
	s.Assert().Equal(res, []string{subroutines.ProvidersecretSubroutineFinalizer})
}

func (s *ProvidersecretTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.KcpsetupSubroutineName)
}

func (suite *ProvidersecretTestSuite) TestConstructor() {
	// create new logger
	suite.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	suite.clientMock = new(mocks.Client)
	helper := &subroutines.Helper{}

	// create new test object
	suite.testObj = subroutines.NewProvidersecretSubroutine(suite.clientMock, helper)
}

func (s *ProvidersecretTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), nil)
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}
