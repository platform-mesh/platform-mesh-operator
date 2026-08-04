package subroutines

import (
	"path/filepath"
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderGotemplateDocs renders a gotemplates/ file through the operator's own
// renderTemplateFile and returns every YAML document as a generic map.
func renderGotemplateDocs(t *testing.T, relPath string, data map[string]interface{}) []map[string]interface{} {
	t.Helper()
	path := filepath.Join("..", "..", "gotemplates", filepath.FromSlash(relPath))

	cfg := logger.DefaultConfig()
	cfg.NoJSON = true
	log, err := logger.New(cfg)
	require.NoError(t, err)

	objs, err := (&DeploymentSubroutine{}).renderTemplateFile(path, data, log)
	require.NoError(t, err)

	out := make([]map[string]interface{}, len(objs))
	for i, obj := range objs {
		out[i] = obj.Object
	}
	return out
}

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
