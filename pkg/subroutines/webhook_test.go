package subroutines_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/openmfp/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
	"github.com/openmfp/openmfp-operator/pkg/subroutines/mocks"
)

type WebhooksTestSuite struct {
	suite.Suite

	testObj    *subroutines.WebhooksSubroutine
	clientMock *mocks.Client

	log *logger.Logger
}

func TestWebhooksTestSuite(t *testing.T) {
	suite.Run(t, new(WebhooksTestSuite))
}

func (suite *WebhooksTestSuite) SetupTest() {
	suite.log, _ = logger.New(logger.DefaultConfig())
	suite.clientMock = new(mocks.Client)
	suite.testObj = subroutines.NewWebhooksSubroutine(suite.clientMock, nil)
}

func (suite *WebhooksTestSuite) TearDownTest() {
	// clear test object
	suite.testObj = nil
}

func (s *WebhooksTestSuite) Test_Constructor() {
	// create new logger
	s.log, _ = logger.New(logger.DefaultConfig())

	// create new mock client
	s.clientMock = new(mocks.Client)

	// create new test object
	s.testObj = subroutines.NewWebhooksSubroutine(s.clientMock, nil)
	s.NotNil(s.testObj)
}

func (s *WebhooksTestSuite) TestFinalizers() {
	res := s.testObj.Finalizers()
	s.Assert().Equal(res, []string{subroutines.WebhooksSubroutineFinalizer})
}

func (s *WebhooksTestSuite) TestGetName() {
	res := s.testObj.GetName()
	s.Assert().Equal(res, subroutines.WebhooksSubroutineName)
}

func (s *WebhooksTestSuite) TestFinalize() {
	res, err := s.testObj.Finalize(context.Background(), &corev1alpha1.OpenMFP{})
	s.Assert().Nil(err)
	s.Assert().Equal(res, ctrl.Result{})
}

func (s *WebhooksTestSuite) TestDefaultProcess() {
	mockedHelper := new(mocks.KcpHelper)
	mockKcpClient := new(mocks.Client)
	mockedHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()

	instance := &corev1alpha1.OpenMFP{
		Spec: corev1alpha1.OpenMFPSpec{
			Kcp: corev1alpha1.Kcp{
				AdminSecretRef: &corev1alpha1.AdminSecretRef{
					SecretRef: corev1alpha1.SecretReference{
						Name:      "test-secret",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
			},
		},
	}

	rootSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	caSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secretCA",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt": []byte("test-ca"),
		},
	}
	mockClient := new(mocks.Client)
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = caSecret
		return nil
	}).Once()

	mutatingWebhook := &v1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-mutating-webhook",
			Namespace: "default",
		},
		Webhooks: []v1.MutatingWebhook{
			{
				Name: "my-mutating-webhook",
				ClientConfig: v1.WebhookClientConfig{
					URL:     nil,
					Service: nil,
				},
				Rules: []v1.RuleWithOperations{
					{
						Operations: []v1.OperationType{
							v1.Create,
						},
					},
				},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             nil,
				TimeoutSeconds:          nil,
				FailurePolicy:           nil,
				NamespaceSelector:       nil,
				ObjectSelector:          nil,
			},
		},
	}

	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*v1.MutatingWebhookConfiguration) = *mutatingWebhook
		return nil
	}).Once()
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, o client.Object, patch client.Patch, opts ...client.PatchOption) error {
		us := o.(*unstructured.Unstructured)
		caBundle := us.Object["webhooks"].([]interface{})[0].(map[string]interface{})["clientConfig"].(map[string]interface{})["caBundle"]
		if caBundle != nil && caBundle == "dGVzdC1jYQ==" {
			return nil
		}
		return fmt.Errorf("CABundle not set")
	}).Once()

	s.testObj = subroutines.NewWebhooksSubroutine(mockClient, mockedHelper)

	res, opErr := s.testObj.Process(context.Background(), instance)
	s.Assert().Nil(opErr)
	s.Assert().Equal(res, ctrl.Result{})

}

func (s *WebhooksTestSuite) TestSyncWebhook() {
	mockedKcpHelper := new(mocks.KcpHelper)
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil).Once()

	rootSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	caSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secretCA",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt": []byte("test-ca"),
		},
	}
	mockClient := new(mocks.Client)
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = caSecret
		return nil
	}).Once()

	mutatingWebhook := &v1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-mutating-webhook",
			Namespace: "default",
		},
		Webhooks: []v1.MutatingWebhook{
			{
				Name: "my-mutating-webhook",
				ClientConfig: v1.WebhookClientConfig{
					URL:     nil,
					Service: nil,
				},
				Rules: []v1.RuleWithOperations{
					{
						Operations: []v1.OperationType{
							v1.Create,
						},
					},
				},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             nil,
				TimeoutSeconds:          nil,
				FailurePolicy:           nil,
				NamespaceSelector:       nil,
				ObjectSelector:          nil,
			},
		},
	}

	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*v1.MutatingWebhookConfiguration) = *mutatingWebhook
		return nil
	}).Once()
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, o client.Object, patch client.Patch, opts ...client.PatchOption) error {
		us := o.(*unstructured.Unstructured)
		caBundle := us.Object["webhooks"].([]interface{})[0].(map[string]interface{})["clientConfig"].(map[string]interface{})["caBundle"]
		if caBundle != nil && caBundle == "dGVzdC1jYQ==" {
			return nil
		}
		return fmt.Errorf("CABundle not set")
	}).Once()
	s.testObj = subroutines.NewWebhooksSubroutine(mockClient, mockedKcpHelper)

	instance := &corev1alpha1.OpenMFP{
		Spec: corev1alpha1.OpenMFPSpec{
			Kcp: corev1alpha1.Kcp{
				AdminSecretRef: &corev1alpha1.AdminSecretRef{
					SecretRef: corev1alpha1.SecretReference{
						Name:      "test-secret",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
				WebhookConfigurations: []corev1alpha1.WebhookConfiguration{
					{
						SecretRef: corev1alpha1.SecretReference{
							Name:      "secret1",
							Namespace: "default",
						},
						SecretData: "ca.crt",
						WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
							ApiVersion: "admissionregistration.k8s.io/v1",
							Kind:       "MutatingWebhookConfiguration",
							Name:       "my-mutating-webhook",
						},
					},
				},
			},
		},
	}

	res, opErr := s.testObj.Process(context.Background(), instance)
	s.Assert().Nil(opErr)
	s.Assert().Equal(res, ctrl.Result{})

}

func (s *WebhooksTestSuite) TestHandleWebhookConfigErrors() {
	// test: Update webhook error
	mockedKcpHelper := new(mocks.KcpHelper)
	mockKcpClient := new(mocks.Client)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(mockKcpClient, nil)

	rootSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	caSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secretCA",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ca.crt": []byte("dGVzdC1jYQ=="),
		},
	}
	mockClient := new(mocks.Client)
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = caSecret
		return nil
	}).Once()

	mutatingWebhook := &v1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-mutating-webhook",
			Namespace: "default",
		},
		Webhooks: []v1.MutatingWebhook{
			{
				Name: "my-mutating-webhook",
				ClientConfig: v1.WebhookClientConfig{
					URL:     nil,
					Service: nil,
				},
				Rules: []v1.RuleWithOperations{
					{
						Operations: []v1.OperationType{
							v1.Create,
						},
					},
				},
				AdmissionReviewVersions: []string{"v1"},
				SideEffects:             nil,
				TimeoutSeconds:          nil,
				FailurePolicy:           nil,
				NamespaceSelector:       nil,
				ObjectSelector:          nil,
			},
		},
	}

	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*v1.MutatingWebhookConfiguration) = *mutatingWebhook
		return nil
	}).Once()
	mockKcpClient.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, o client.Object, p client.Patch, opts ...client.PatchOption) error {
		return fmt.Errorf("Forced test error")
	}).Once()

	s.testObj = subroutines.NewWebhooksSubroutine(mockClient, mockedKcpHelper)

	instance := &corev1alpha1.OpenMFP{
		Spec: corev1alpha1.OpenMFPSpec{
			Kcp: corev1alpha1.Kcp{
				AdminSecretRef: &corev1alpha1.AdminSecretRef{
					SecretRef: corev1alpha1.SecretReference{
						Name:      "test-secret",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
				WebhookConfigurations: []corev1alpha1.WebhookConfiguration{
					{
						SecretRef: corev1alpha1.SecretReference{
							Name:      "secret1",
							Namespace: "default",
						},
						SecretData: "ca.crt",
						WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
							ApiVersion: "admissionregistration.k8s.io/v1",
							Kind:       "MutatingWebhookConfiguration",
							Name:       "my-mutating-webhook",
						},
					},
				},
			},
		},
	}

	_, opErr := s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal(opErr.Err().Error(), "Forced test error")

	// test: Get webhook error
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = caSecret
		return nil
	}).Once()
	mockKcpClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		return fmt.Errorf("Forced webhook update error")
	}).Once()

	_, opErr = s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal(opErr.Err().Error(), "Forced webhook update error")

	// test: Failed to get caData from secret
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	caSecretNokey := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secretCA",
			Namespace: "default",
		},
		Data: map[string][]byte{},
	}
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = caSecretNokey
		return nil
	}).Once()

	_, opErr = s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal("Failed to get caData from secret", opErr.Err().Error())

	// test: Failed to get ca secret
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		return fmt.Errorf("Failed to get ca secret")
	}).Once()

	_, opErr = s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal("Failed to get ca secret", opErr.Err().Error())

}

func (s *WebhooksTestSuite) TestHandleKcpError() {
	// test: error getting kcp client
	mockedKcpHelper := new(mocks.KcpHelper)
	mockClient := new(mocks.Client)
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("Failed to get kcp client"))
	rootSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"kubeconfig": secretKubeconfigData,
		},
	}
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecret
		return nil
	}).Once()

	s.testObj = subroutines.NewWebhooksSubroutine(mockClient, mockedKcpHelper)

	instance := &corev1alpha1.OpenMFP{
		Spec: corev1alpha1.OpenMFPSpec{
			Kcp: corev1alpha1.Kcp{
				AdminSecretRef: &corev1alpha1.AdminSecretRef{
					SecretRef: corev1alpha1.SecretReference{
						Name:      "test-secret",
						Namespace: "default",
					},
					Key: "kubeconfig",
				},
				WebhookConfigurations: []corev1alpha1.WebhookConfiguration{
					{
						SecretRef: corev1alpha1.SecretReference{
							Name:      "secret1",
							Namespace: "default",
						},
						SecretData: "ca.crt",
						WebhookRef: corev1alpha1.KCPAPIVersionKindRef{
							ApiVersion: "admissionregistration.k8s.io/v1",
							Kind:       "MutatingWebhookConfiguration",
							Name:       "my-mutating-webhook",
						},
					},
				},
			},
		},
	}

	_, opErr := s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal("Failed to get kcp client", opErr.Err().Error())

	// test: error secret key
	mockedKcpHelper.EXPECT().NewKcpClient(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("Failed to get kcp client"))
	rootSecretWrongkey := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret1",
			Namespace: "default",
		},
		Data: map[string][]byte{},
	}
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		*o.(*corev1.Secret) = rootSecretWrongkey
		return nil
	}).Once()

	s.testObj = subroutines.NewWebhooksSubroutine(mockClient, mockedKcpHelper)

	_, opErr = s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal("invalid configuration: no configuration has been provided, try setting KUBERNETES_MASTER environment variable", opErr.Err().Error())

	// test: error getting secret
	mockClient.EXPECT().Get(
		mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, nn types.NamespacedName, o client.Object, opts ...client.GetOption,
	) error {
		return fmt.Errorf("Failed to get secret")
	}).Once()

	_, opErr = s.testObj.HandleWebhookConfig(context.Background(), instance, instance.Spec.Kcp.WebhookConfigurations[0])
	s.Assert().NotNil(opErr)
	s.Assert().Equal("Failed to get secret", opErr.Err().Error())

}
