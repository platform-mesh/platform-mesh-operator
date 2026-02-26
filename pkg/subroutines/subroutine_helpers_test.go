package subroutines_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/platform-mesh/golang-commons/context/keys"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/mocks"
)

type HelperTestSuite struct {
	suite.Suite

	subroutines.KcpHelper
}

func TestHelperTestSuite(t *testing.T) {
	suite.Run(t, new(HelperTestSuite))
}

func (s *HelperTestSuite) TestGetWorkspaceName() {
	tests := []struct {
		input       string
		expected    string
		expectError bool
	}{
		{"01-platform-mesh-system", "platform-mesh-system", false},
		{"99-abc123", "abc123", false},
		{"00-", "", true},
		{"platform-mesh-system", "", true},
		{"01platform-mesh-system", "01platform-mesh-system", true},
		{"01-platform-mesh_system", "01-platform-mesh_system", true},
		{"01-platform-mesh-system-extra", "platform-mesh-system-extra", false},
		{"1-platform-mesh-system", "1-platform-mesh-system", true},
		{"01-", "-", true},
		{"", "", true},
		{"01-Platform-Mesh-System", "Platform-Mesh-System", false},
		{"01-platform-mesh/", "01-platform-mesh/", true},
		{"../../manifests/kcp/02-orgs", "orgs", false},
		{"/operator/manifests/kcp/02-orgs", "orgs", false},
	}

	for _, tt := range tests {
		s.T().Run(tt.input, func(t *testing.T) {
			result, err := subroutines.GetWorkspaceName(tt.input)
			if tt.expectError {
				s.Assert().Error(err, "input: %q", tt.input)
			} else {
				s.Assert().NoError(err, "input: %q", tt.input)
				s.Assert().Equal(tt.expected, result, "input: %q", tt.input)
			}
		})
	}
}

func (s *HelperTestSuite) TestListFiles() {
	// Create a temporary directory
	dir, err := os.MkdirTemp("", "listfiles-test")
	s.Require().NoError(err)
	defer os.RemoveAll(dir) //nolint:errcheck

	// Create some files and subdirectories
	files := []string{"file1.txt", "file2.yaml", "file3"}
	subdirs := []string{"subdir1", "subdir2"}
	for _, fname := range files {
		f, err := os.CreateTemp(dir, fname)
		s.Require().NoError(err)
		f.Close() //nolint:errcheck
	}
	for _, dname := range subdirs {
		_, err := os.MkdirTemp(dir, dname)
		s.Require().NoError(err)
	}

	// Get the expected file names (os.CreateTemp adds random suffix)
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	expected := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			expected = append(expected, entry.Name())
		}
	}

	// Call ListFiles
	result, err := subroutines.ListFiles(dir)
	s.Require().NoError(err)
	s.ElementsMatch(expected, result)
}

func (s *HelperTestSuite) TestListFiles_DirectoryNotExist() {
	// Call ListFiles on a non-existent directory
	result, err := subroutines.ListFiles("/nonexistent/path/to/dir")
	s.Error(err)
	s.Empty(result)
	s.Contains(err.Error(), "Failed to read directory")
}

func (s *HelperTestSuite) TestListFiles_EmptyDirectory() {
	dir, err := os.MkdirTemp("", "listfiles-empty")
	s.Require().NoError(err)
	defer os.RemoveAll(dir) //nolint:errcheck

	result, err := subroutines.ListFiles(dir)
	s.NoError(err)
	s.Empty(result)
}

func (s *HelperTestSuite) TestIsWorkspace() {
	tests := []struct {
		dir      string
		expected bool
	}{
		{"01-platform-mesh-system", true},
		{"99-abc123", true},
		{"00-", false},
		{"platform-mesh-system", false},
		{"01platform-mesh-system", false},
		{"01-platform-mesh_system", false},
		{"01-platform-mesh-system-extra", true},
		{"1-platform-mesh-system", false},
		{"01-", false},
		{"", false},
		{"01-PlatformMesh-System", true},
		{"01-platform-mesh-system/", false},
	}

	for _, tt := range tests {
		s.T().Run(tt.dir, func(t *testing.T) {
			result := subroutines.IsWorkspace(tt.dir)
			s.Assert().Equal(tt.expected, result, "dir: %q", tt.dir)
		})
	}
}

func (s *HelperTestSuite) TestConvertToUnstructured() {
	// Create a simple MutatingWebhookConfiguration
	webhook := admissionv1.MutatingWebhookConfiguration{}
	webhook.Name = "test-webhook"
	webhook.Namespace = "test-namespace"

	// Add a webhook to the configuration
	webhook.Webhooks = []admissionv1.MutatingWebhook{
		{
			Name: "test.webhook.example.com",
			ClientConfig: admissionv1.WebhookClientConfig{
				URL: strPtr("https://example.com/webhook"),
			},
			Rules: []admissionv1.RuleWithOperations{
				{
					Operations: []admissionv1.OperationType{admissionv1.Create},
					Rule: admissionv1.Rule{
						APIGroups:   []string{"apps"},
						APIVersions: []string{"v1"},
						Resources:   []string{"deployments"},
					},
				},
			},
		},
	}

	// Convert to unstructured
	unstructuredObj, err := subroutines.ConvertToUnstructured(webhook)

	// Verify no error occurred
	s.Assert().NoError(err)
	s.Assert().NotNil(unstructuredObj)

	// Verify the kind and apiVersion were set correctly
	s.Assert().Equal("MutatingWebhookConfiguration", unstructuredObj.GetKind())
	s.Assert().Equal("admissionregistration.k8s.io/v1", unstructuredObj.GetAPIVersion())

	// Verify managed fields were cleared
	s.Assert().Nil(unstructuredObj.GetManagedFields())

	// Verify name was preserved
	s.Assert().Equal("test-webhook", unstructuredObj.GetName())

	// Verify structure was preserved
	webhooks, found, err := unstructured.NestedSlice(unstructuredObj.Object, "webhooks")
	s.Assert().NoError(err)
	s.Assert().True(found)
	s.Assert().Len(webhooks, 1)

	webhookMap, ok := webhooks[0].(map[string]interface{})
	s.Assert().True(ok)
	name, found, err := unstructured.NestedString(webhookMap, "name")
	s.Assert().NoError(err)
	s.Assert().True(found)
	s.Assert().Equal("test.webhook.example.com", name)
}

// Helper function to create string pointers
func strPtr(s string) *string {
	return &s
}

func (s *HelperTestSuite) TestReplaceTemplate_ParseError() {
	templateData := map[string]any{
		"Name": "World",
	}
	// Invalid template syntax {{ .Name
	templateBytes := []byte("Hello, {{ .Name")

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().Error(err)
	s.Assert().Contains(err.Error(), "Failed to parse template")
	s.Assert().Empty(result)
}

func (s *HelperTestSuite) TestReplaceTemplate_ExecuteError() {
	templateData := map[string]any{
		"Name": "World",
	}
	// Template tries to access a non-existent field in a struct (if data were a struct)
	// or uses an invalid action. Let's use an invalid action.
	templateBytes := []byte("Hello, {{ .Name }}. {{ if true }} Mismatched brackets")

	// First, check parsing error because the template is malformed
	_, parseErr := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().Error(parseErr)
	s.Assert().Contains(parseErr.Error(), "Failed to parse template")

	// Test case with missing key (text/template default behavior is to insert <no value>)
	templateBytesMissingKey := []byte("Hello, {{ .Name }}. Your ID is {{ .ID }}.")
	expectedMissingKey := []byte("Hello, World. Your ID is <no value>.")
	resultMissingKey, errMissingKey := subroutines.ReplaceTemplate(templateData, templateBytesMissingKey)
	s.Assert().NoError(errMissingKey)
	s.Assert().Equal(expectedMissingKey, resultMissingKey)

}

func (s *HelperTestSuite) TestReplaceTemplate_EmptyData() {
	templateData := map[string]any{}
	templateBytes := []byte("Hello, {{ .Name }}!")
	expected := []byte("Hello, <no value>!") // Default behavior for missing keys

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().NoError(err)
	s.Assert().Equal(expected, result)
}

func (s *HelperTestSuite) TestReplaceTemplate_EmptyTemplate() {
	templateData := map[string]any{
		"Name": "World",
	}
	templateBytes := []byte{}
	expected := []byte{}

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().NoError(err)
	s.Assert().Equal(expected, result)
}

func (s *HelperTestSuite) TestReplaceTemplate_Success() {
	templateData := map[string]any{
		"Name": "World",
		"Age":  "30",
	}
	templateBytes := []byte("Hello, {{ .Name }}! You are {{ .Age }}.")
	expected := []byte("Hello, World! You are 30.")

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().NoError(err)
	s.Assert().Equal(expected, result)
}

func (suite *HelperTestSuite) SetupTest() {
	suite.KcpHelper = &subroutines.Helper{}
}

func (s *HelperTestSuite) TestConstructorError() {
	client, err := s.NewKcpClient(&rest.Config{}, "")
	s.Assert().Error(err)
	s.Assert().Nil(client)
}

func (s *HelperTestSuite) TestConstructorOK() {
	client, err := s.NewKcpClient(&rest.Config{
		Host: "http://server:1234",
	}, "")
	s.Assert().NoError(err)
	s.Assert().NotNil(client)
}

func (s *HelperTestSuite) TestApplyManifestFromFile() {

	cl := new(mocks.Client)
	// SSA Patch call (no Get needed)
	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err := subroutines.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-platform-mesh-system.yaml", cl, make(map[string]any), "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
	s.Assert().Nil(err)

	err = subroutines.ApplyManifestFromFile(context.TODO(), "invalid", nil, make(map[string]any), "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
	s.Assert().Error(err)

	err = subroutines.ApplyManifestFromFile(context.TODO(), "./kcpsetup.go", nil, make(map[string]any), "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
	s.Assert().Error(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("error")).Once()
	err = subroutines.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-platform-mesh-system.yaml", cl, make(map[string]any), "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
	s.Assert().Error(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	err = subroutines.ApplyManifestFromFile(context.TODO(), "../../manifests/kcp/workspace-orgs.yaml", cl, make(map[string]any), "root:orgs", &corev1alpha1.PlatformMesh{})
	s.Assert().Nil(err)

	cl.EXPECT().Patch(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	templateData := map[string]any{
		".account-operator.webhooks.platform-mesh.io.ca-bundle": "CABundle",
	}

	operatorCfg := config.OperatorConfig{
		KCP: config.OperatorConfig{}.KCP,
	}
	ctx := context.WithValue(context.TODO(), keys.ConfigCtxKey, operatorCfg)
	err = subroutines.ApplyManifestFromFile(ctx, "../../manifests/kcp/04-platform-mesh-system/mutatingwebhookconfiguration-admissionregistration.k8s.io.yaml", cl, templateData, "root:platform-mesh-system", &corev1alpha1.PlatformMesh{})
	s.Assert().Nil(err)
}

func (s *HelperTestSuite) TestPlatformMeshExtraProviderConnections() {
	// Simulate a PlatformMesh resource with extraProviderConnections:
	//   spec:
	//     kcp:
	//       extraProviderConnections:
	//         - endpointSliceName: ""
	//           path: root:providers:httpbin-provider
	//           secret: httpbin-kubeconfig
	//           namespace: example-httpbin-provider
	//           external: true
	instance := &corev1alpha1.PlatformMesh{}
	instance.Name = "test-platform-mesh"
	instance.Namespace = "default"
	instance.Spec.Exposure = &corev1alpha1.ExposureConfig{
		BaseDomain: "example.com",
		Port:       1234,
		Protocol:   "https",
	}
	instance.Spec.Kcp.ExtraProviderConnections = []corev1alpha1.ProviderConnection{
		{
			EndpointSliceName: ptr.To(""),
			Path:              "root:providers:httpbin-provider",
			Secret:            "httpbin-kubeconfig",
			Namespace:         ptr.To("example-httpbin-provider"),
			External:          true,
		},
	}

	// Setup mocks
	clientMock := new(mocks.Client)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = corev1alpha1.AddToScheme(scheme)
	clientMock.EXPECT().Scheme().Return(scheme).Maybe()

	// Capture the secret passed to Patch so we can assert on it after HandleProviderConnection returns
	var capturedSecret *corev1.Secret
	clientMock.EXPECT().
		Patch(mock.Anything,
			mock.MatchedBy(func(obj client.Object) bool {
				sec, ok := obj.(*corev1.Secret)
				if !ok {
					return false
				}
				return sec.Name == "httpbin-kubeconfig" && sec.Namespace == "example-httpbin-provider"
			}),
			mock.Anything,
			mock.Anything,
			mock.Anything).
		RunAndReturn(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
			capturedSecret = obj.(*corev1.Secret).DeepCopy()
			return nil
		}).Once()

	// Setup operator config and context
	operatorCfg := config.OperatorConfig{}
	operatorCfg.KCP.FrontProxyName = "frontproxy"
	operatorCfg.KCP.FrontProxyPort = "6443"
	operatorCfg.KCP.Namespace = "platform-mesh-system"

	logCfg := logger.DefaultConfig()
	logCfg.Level = "debug"
	logCfg.NoJSON = true
	log, _ := logger.New(logCfg)

	ctx := context.WithValue(context.Background(), keys.LoggerCtxKey, log)
	ctx = context.WithValue(ctx, keys.ConfigCtxKey, operatorCfg)

	// Build a rest.Config to pass to HandleProviderConnection
	restCfg := &rest.Config{
		Host: "https://frontproxy.platform-mesh-system:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CertData: []byte("dummy-cert"),
			KeyData:  []byte("dummy-key"),
			CAData:   []byte("dummy-ca"),
		},
	}

	// Create the ProvidersecretSubroutine and invoke HandleProviderConnection
	testObj := subroutines.NewProviderSecretSubroutine(clientMock, &subroutines.Helper{}, nil)

	pc := instance.Spec.Kcp.ExtraProviderConnections[0]
	res, opErr := testObj.HandleProviderConnection(ctx, instance, pc, restCfg)
	s.Require().Nil(opErr)
	s.Assert().False(res.Requeue)

	// Validate the captured secret after HandleProviderConnection returns
	clientMock.AssertExpectations(s.T())
	s.Require().NotNil(capturedSecret, "Patch should have been called with a secret")
	s.Assert().Equal("httpbin-kubeconfig", capturedSecret.Name)
	s.Assert().Equal("example-httpbin-provider", capturedSecret.Namespace)

	// Parse the kubeconfig from the secret and validate the server: field
	kubeconfigData, ok := capturedSecret.Data["kubeconfig"]
	s.Require().True(ok, "secret should contain kubeconfig key")
	s.Require().NotEmpty(kubeconfigData, "kubeconfig data should not be empty")

	cfg, err := clientcmd.Load(kubeconfigData)
	s.Require().NoError(err, "kubeconfig should be valid")

	expectedServer := fmt.Sprintf("https://example.com:1234/clusters/%s",
		"root:providers:httpbin-provider")

	s.Require().Len(cfg.Clusters, 1, "kubeconfig should have exactly one cluster")
	cluster, ok := cfg.Clusters["default-cluster"]
	s.Require().True(ok, "kubeconfig should have a 'default-cluster' entry")
	s.Assert().Equal(expectedServer, cluster.Server,
		"server field should be front-proxy host with provider path")
}
