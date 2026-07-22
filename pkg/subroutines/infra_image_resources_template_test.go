package subroutines

import (
	"path/filepath"
	"testing"

	"github.com/platform-mesh/golang-commons/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderGotemplateDocs renders a gotemplates/ template file with the given flat
// template vars and returns every YAML document as a generic map. It reuses
// DeploymentSubroutine.renderTemplateFile so tests exercise the same parsing and
// multi-document-splitting logic the operator runs in production.
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

// renderInfraTemplate renders a gotemplates/infra/runtime template file and returns
// its first YAML document.
func renderInfraTemplate(t *testing.T, relPath string, data map[string]interface{}) map[string]interface{} {
	t.Helper()
	docs := renderGotemplateDocs(t, "infra/runtime/"+relPath, data)
	require.NotEmpty(t, docs, "template must render at least one document")
	return docs[0]
}

func infraOCMVars() map[string]interface{} {
	return map[string]interface{}{
		"helmReleaseNamespace": "platform-mesh-system",
		"releaseNamespace":     "platform-mesh-system",
		"ocm": map[string]interface{}{
			"component":     map[string]interface{}{"name": "platform-mesh"},
			"repo":          map[string]interface{}{"name": "platform-mesh"},
			"referencePath": []interface{}{},
		},
	}
}

func asfOf(t *testing.T, obj map[string]interface{}) map[string]interface{} {
	t.Helper()
	spec, ok := obj["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be present")
	asf, ok := spec["additionalStatusFields"].(map[string]interface{})
	require.True(t, ok, "spec.additionalStatusFields must be rendered")
	return asf
}

// Test_CertManagerImageTemplates_FullLocalization asserts the three cert-manager image
// Resources carry the full split-schema additionalStatusFields (registry/repository/tag/digest)
// so the operator injects the localized registry+repository, not just the tag. cert-manager's
// chart uses a split image schema that accepts a digest.
func Test_CertManagerImageTemplates_FullLocalization(t *testing.T) {
	data := infraOCMVars()
	data["certManager"] = map[string]interface{}{"enabled": true, "name": "cert-manager"}

	cases := []struct {
		file     string
		resource string
	}{
		{"cert-manager/resource-controller.yaml", "controller-image"},
		{"cert-manager/resource-webhook.yaml", "webhook-image"},
		{"cert-manager/resource-cainjector.yaml", "cainjector-image"},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			obj := renderInfraTemplate(t, tc.file, data)

			byRef := obj["spec"].(map[string]interface{})["resource"].(map[string]interface{})["byReference"].(map[string]interface{})
			assert.Equal(t, tc.resource, byRef["resource"].(map[string]interface{})["name"], "must reference the localized OCM image resource")

			asf := asfOf(t, obj)
			assert.Equal(t, "resource.access.imageReference.toOCI().registry", asf["registry"])
			assert.Equal(t, "resource.access.imageReference.toOCI().repository", asf["repository"])
			assert.Equal(t, "resource.access.imageReference.toOCI().tag", asf["tag"])
			assert.Equal(t, "resource.access.imageReference.toOCI().digest", asf["digest"], "cert-manager accepts a digest field")
		})
	}
}

// Test_EtcdDruidImageTemplate_CombinedNoDigest asserts the etcd-druid image Resource is
// flagged image-ref: combined (its chart exposes only a single host-qualified
// image.repository + image.tag) and therefore does NOT emit a digest field.
func Test_EtcdDruidImageTemplate_CombinedNoDigest(t *testing.T) {
	data := infraOCMVars()
	data["etcdDruid"] = map[string]interface{}{"enabled": true, "name": "etcd-druid"}

	obj := renderInfraTemplate(t, "etcd-druid/resource-image.yaml", data)

	annotations := obj["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})
	assert.Equal(t, "combined", annotations["image-ref"], "etcd-druid must use combined image-ref")
	assert.Equal(t, "image.tag", annotations["path"])

	asf := asfOf(t, obj)
	assert.Equal(t, "resource.access.imageReference.toOCI().registry", asf["registry"])
	assert.Equal(t, "resource.access.imageReference.toOCI().repository", asf["repository"])
	assert.Equal(t, "resource.access.imageReference.toOCI().tag", asf["tag"])
	_, hasDigest := asf["digest"]
	assert.False(t, hasDigest, "combined mode must not emit a digest (registry is folded into repository)")
}

// Test_OtelOperatorImageTemplate_ThreeCombinedNoDigest asserts the opentelemetry-operator
// template emits three combined image Resources (manager, collector, target-allocator),
// each folding the registry into the repository (no digest — its chart's image schema is
// strict and host-qualified).
func Test_OtelOperatorImageTemplate_ThreeCombinedNoDigest(t *testing.T) {
	data := infraOCMVars()
	data["opentelemetryOperator"] = map[string]interface{}{
		"enabled":          true,
		"name":             "opentelemetry-operator",
		"ocmComponentName": "opentelemetry-operator",
	}

	docs := renderGotemplateDocs(t, "infra/runtime/opentelemetry-operator/resource-image.yaml", data)
	require.Len(t, docs, 3, "manager + collector + target-allocator")

	want := map[string]string{ // resource name -> values path
		"manager-image":          "manager.image.tag",
		"collector-image":        "manager.collectorImage.tag",
		"target-allocator-image": "manager.targetAllocatorImage.tag",
	}
	seen := map[string]bool{}
	for _, obj := range docs {
		meta := obj["metadata"].(map[string]interface{})
		ann := meta["annotations"].(map[string]interface{})
		assert.Equal(t, "combined", ann["image-ref"], "every otel image is combined")
		assert.Equal(t, "opentelemetry-operator", meta["labels"].(map[string]interface{})["for"])

		resName := obj["spec"].(map[string]interface{})["resource"].(map[string]interface{})["byReference"].(map[string]interface{})["resource"].(map[string]interface{})["name"].(string)
		wantPath, ok := want[resName]
		require.True(t, ok, "unexpected resource name %q", resName)
		assert.Equal(t, wantPath, ann["path"], "path must match the chart's combined value for %s", resName)
		seen[resName] = true

		asf := asfOf(t, obj)
		assert.Equal(t, "resource.access.imageReference.toOCI().registry", asf["registry"])
		assert.Equal(t, "resource.access.imageReference.toOCI().repository", asf["repository"])
		assert.Equal(t, "resource.access.imageReference.toOCI().tag", asf["tag"])
		_, hasDigest := asf["digest"]
		assert.False(t, hasDigest, "combined mode must not emit a digest for %s", resName)
	}
	assert.Len(t, seen, 3, "all three distinct resources rendered")
}

// Test_TraefikImageTemplate_SplitNoDigest asserts the new traefik image Resource references
// the OCM resource named "image" and emits registry/repository/tag WITHOUT a digest, because
// traefik's chart image schema is strict (additionalProperties: false) and has no digest field.
func Test_TraefikImageTemplate_SplitNoDigest(t *testing.T) {
	data := infraOCMVars()
	data["traefik"] = map[string]interface{}{"enabled": true, "name": "traefik"}

	obj := renderInfraTemplate(t, "traefik/resource-image.yaml", data)

	metadata := obj["metadata"].(map[string]interface{})
	assert.Equal(t, "traefik-image", metadata["name"])
	assert.Equal(t, "traefik", metadata["labels"].(map[string]interface{})["for"], "must target the traefik HelmRelease")

	byRef := obj["spec"].(map[string]interface{})["resource"].(map[string]interface{})["byReference"].(map[string]interface{})
	assert.Equal(t, "image", byRef["resource"].(map[string]interface{})["name"], "traefik's OCM image resource is named 'image'")

	asf := asfOf(t, obj)
	assert.Equal(t, "resource.access.imageReference.toOCI().registry", asf["registry"])
	assert.Equal(t, "resource.access.imageReference.toOCI().repository", asf["repository"])
	assert.Equal(t, "resource.access.imageReference.toOCI().tag", asf["tag"])
	_, hasDigest := asf["digest"]
	assert.False(t, hasDigest, "traefik's strict image schema rejects a digest field")
}
