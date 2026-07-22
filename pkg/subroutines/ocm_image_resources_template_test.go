package subroutines

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_OcmImageResourcesTemplate renders gotemplates/components/runtime/ocm-image-resources.yaml
// with a minimal profile and asserts the produced Resource carries the localized-image
// machinery: additionalStatusFields (toOCI CEL expressions) and the annotation passthrough
// consumed by the ResourceSubroutine.
func Test_OcmImageResourcesTemplate(t *testing.T) {
	data := map[string]interface{}{
		"releaseNamespace": "platform-mesh-system",
		"values": map[string]interface{}{
			"ocm": map[string]interface{}{
				"component":     map[string]interface{}{"name": "platform-mesh"},
				"repo":          map[string]interface{}{"name": "platform-mesh"},
				"referencePath": []interface{}{map[string]interface{}{"name": "github.com/platform-mesh/account-operator"}},
			},
			"services": map[string]interface{}{
				"account-operator": map[string]interface{}{
					"enabled": true,
					"imageResources": []interface{}{
						map[string]interface{}{
							"annotations": map[string]interface{}{
								"repo":     "oci",
								"artifact": "image",
								"for":      "account-operator",
							},
						},
					},
				},
			},
		},
	}

	docs := renderGotemplateDocs(t, "components/runtime/ocm-image-resources.yaml", data)
	require.Len(t, docs, 1, "one imageResource must render exactly one Resource")
	obj := docs[0]

	metadata := obj["metadata"].(map[string]interface{})
	assert.Equal(t, "account-operator-image", metadata["name"])
	annotations := metadata["annotations"].(map[string]interface{})
	assert.Equal(t, "account-operator", annotations["for"])

	spec := obj["spec"].(map[string]interface{})
	asf, ok := spec["additionalStatusFields"].(map[string]interface{})
	require.True(t, ok, "spec.additionalStatusFields must be rendered")
	assert.Equal(t, "resource.access.imageReference.toOCI().registry", asf["registry"])
	assert.Equal(t, "resource.access.imageReference.toOCI().repository", asf["repository"])
	assert.Equal(t, "resource.access.imageReference.toOCI().tag", asf["tag"])
	assert.Equal(t, "resource.access.imageReference.toOCI().digest", asf["digest"])
}
