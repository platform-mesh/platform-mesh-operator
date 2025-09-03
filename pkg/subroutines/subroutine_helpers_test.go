package subroutines_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
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
	templateData := map[string]string{
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
	templateData := map[string]string{
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
	templateData := map[string]string{}
	templateBytes := []byte("Hello, {{ .Name }}!")
	expected := []byte("Hello, <no value>!") // Default behavior for missing keys

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().NoError(err)
	s.Assert().Equal(expected, result)
}

func (s *HelperTestSuite) TestReplaceTemplate_EmptyTemplate() {
	templateData := map[string]string{
		"Name": "World",
	}
	templateBytes := []byte{}
	expected := []byte{}

	result, err := subroutines.ReplaceTemplate(templateData, templateBytes)
	s.Assert().NoError(err)
	s.Assert().Equal(expected, result)
}

func (s *HelperTestSuite) TestReplaceTemplate_Success() {
	templateData := map[string]string{
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
