package subroutines_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/openmfp/openmfp-operator/pkg/subroutines"
)

type HelperTestSuite struct {
	suite.Suite

	subroutines.KcpHelper
}

func TestHelperTestSuite(t *testing.T) {
	suite.Run(t, new(HelperTestSuite))
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

func (s *HelperTestSuite) TestReplaceManifests() {
	templateData := map[string]string{
		"e2e-test-webhook.webhooks.core.openmfp.org.ca-bundle": "dGVzdA==",
		"apiExportRootShardsKcpIoIdentityHash":                 "054a9b98bdb5c420e6749ff60975f5fd3cddd61859c730c571ebb3e95acc015e",
		"apiExportRootTenancyKcpIoIdentityHash":                "c52f4c4461a66afedd3cb406e61faeff350f15d3c727dce1961b8f53470a6699",
		"apiExportRootTopologyKcpIoIdentityHash":               "6b429dea42adabdbb7656f4c3a9b85125ae72543adf446dfaad93f22da58a859",
	}

	files := []string{
		"../../test/e2e/e2e-test-webhook.yaml",
		"../../setup/01-openmfp-system/apiexport-core.openmfp.org.yaml",
		"../../setup/01-openmfp-system/apiexport-fga.openmfp.org.yaml",
		"../../setup/01-openmfp-system/apiexport-kcp.io.yaml",
		"../../setup/01-openmfp-system/apiexportendpointslice-core.openmfp.org.yaml",
	}

	results := []string{
		`apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: e2e-test-webhook.webhooks.core.openmfp.org
webhooks:
  - admissionReviewVersions:
      - v1
    clientConfig:
      caBundle: dGVzdA==
      url: https://openmfp-account-operator-webhook.openmfp-system.svc:9443/mutate-core-openmfp-org-v1alpha1-account
    failurePolicy: Fail
    name: maccount.kb.io
    rules:
      - apiGroups:
          - core.openmfp.org
        apiVersions:
          - v1alpha1
        operations:
          - DELETE
        resources:
          - accounts
    sideEffects: None
    timeoutSeconds: 30
`,
		`apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: core.openmfp.org
spec:
  latestResourceSchemas:
    - v250516-0b27c30.accountinfos.core.openmfp.org
    - v250226-290f38d.accounts.core.openmfp.org
  permissionClaims:
    - all: true
      resource: namespaces
    - all: true
      group: tenancy.kcp.io
      identityHash: c52f4c4461a66afedd3cb406e61faeff350f15d3c727dce1961b8f53470a6699
      resource: workspaces
    - all: true
      group: tenancy.kcp.io
      identityHash: c52f4c4461a66afedd3cb406e61faeff350f15d3c727dce1961b8f53470a6699
      resource: workspacetypes
status: {}
`,
		`apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: fga.openmfp.org
spec:
  latestResourceSchemas:
  - v250401-5769f6b.authorizationmodels.fga.openmfp.org
  - v250401-5769f6b.stores.fga.openmfp.org
  permissionClaims:
  - all: true
    group: apis.kcp.io
    identityHash: ""
    resource: apiexports
  - all: true
    group: apis.kcp.io
    identityHash: ""
    resource: apiresourceschemas
status: {}`,
		`apiVersion: apis.kcp.io/v1alpha1
kind: APIExport
metadata:
  name: kcp.io
spec:
  permissionClaims:
    - resource: apibindings
      group: apis.kcp.io
      all: true
      identityHash: ""
    - resource: workspaces
      group: tenancy.kcp.io
      all: true
      identityHash: c52f4c4461a66afedd3cb406e61faeff350f15d3c727dce1961b8f53470a6699
`,
		`kind: APIExportEndpointSlice
apiVersion: apis.kcp.io/v1alpha1
metadata:
  name: core.openmfp.org
spec:
  export:
    path: root:openmfp-system
    name: core.openmfp.org
`,
	}

	for i, path := range files {
		manifestBytes, err := os.ReadFile(path)
		s.Assert().NoError(err)
		result, err := subroutines.ReplaceTemplate(templateData, manifestBytes)
		s.Assert().NoError(err)
		s.Assert().Equal(results[i], string(result))
	}
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
