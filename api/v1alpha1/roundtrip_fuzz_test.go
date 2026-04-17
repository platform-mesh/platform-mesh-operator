package v1alpha1

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
)

func FuzzPlatformMeshRoundTrip(f *testing.F) {
	f.Add([]byte(`{
		"spec": {
			"exposure": {
				"baseDomain": "example.com",
				"port": 443,
				"protocol": "https"
			},
			"kcp": {
				"providerConnections": [{
					"path": "root:orgs:default",
					"secret": "provider-kubeconfig",
					"external": false
				}],
				"extraWorkspaces": [{
					"path": "root:orgs:extra",
					"type": {"name": "universal", "path": "root"}
				}]
			},
			"ocm": {
				"repo": {"name": "platform-mesh"},
				"component": {"name": "platform-mesh"},
				"referencePath": [{"name": "ref1"}]
			},
			"featureToggles": [{"name": "feature-a", "parameters": {"key": "val"}}]
		}
	}`))
	f.Add([]byte(`{
		"spec": {
			"kcp": {
				"providerConnections": [{
					"path": "root:orgs:prod",
					"secret": "prod-secret",
					"external": true,
					"adminAuth": true
				}],
				"extraDefaultAPIBindings": [{
					"workspaceTypePath": "root:types",
					"export": "my-export",
					"path": "root:orgs"
				}]
			},
			"wait": {
				"resourceTypes": [{
					"name": "my-deploy",
					"namespace": "default"
				}]
			}
		}
	}`))
	f.Add([]byte(`{}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzRoundTrip(t, data, &PlatformMesh{}, &PlatformMesh{})
	})
}

// fuzzRoundTrip unmarshals arbitrary JSON into obj, marshals it back, unmarshals
// into obj2, and checks semantic equality. We use equality.Semantic.DeepEqual from
// k8s.io/apimachinery which treats nil and empty slices/maps as equivalent — the
// standard Kubernetes comparison semantic for API objects.
func fuzzRoundTrip[T any](t *testing.T, data []byte, obj *T, obj2 *T) {
	t.Helper()

	if err := json.Unmarshal(data, obj); err != nil {
		return
	}

	roundtripped, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	if err := json.Unmarshal(roundtripped, obj2); err != nil {
		t.Fatalf("failed to unmarshal roundtripped data: %v", err)
	}

	if !equality.Semantic.DeepEqual(obj, obj2) {
		t.Errorf("roundtrip mismatch for %T", obj)
	}
}
