package subroutines

import (
	"strings"
	"sync"

	"sigs.k8s.io/yaml"
)

// ImageVersion represents a version managed by an OCM Resource for a specific Application helm value path.
type ImageVersion struct {
	// Path is the dot-separated path in helm values (e.g., "kcp.image.tag")
	Path string
	// Version is the resolved version from the OCM Resource status (e.g., "v0.30.0")
	Version string
}

// ImageVersionStore is a thread-safe in-memory store that tracks image versions
// resolved by the ResourceSubroutine. The DeploymentSubroutine reads from this store
// to merge Resource-managed versions into ArgoCD Application helm values before applying,
// preventing the DeploymentSubroutine from overwriting versions set by the ResourceSubroutine.
type ImageVersionStore struct {
	mu sync.RWMutex
	// versions maps "namespace/appName" -> list of ImageVersion entries
	versions map[string][]ImageVersion
}

// NewImageVersionStore creates a new ImageVersionStore.
func NewImageVersionStore() *ImageVersionStore {
	return &ImageVersionStore{
		versions: make(map[string][]ImageVersion),
	}
}

// Set stores or updates an image version for a given Application.
// appName is the ArgoCD Application name, namespace is its namespace.
// If an entry with the same path already exists, it is updated.
func (s *ImageVersionStore) Set(namespace, appName, path, version string) {
	key := namespace + "/" + appName
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := s.versions[key]
	for i, e := range entries {
		if e.Path == path {
			entries[i].Version = version
			s.versions[key] = entries
			return
		}
	}
	s.versions[key] = append(entries, ImageVersion{Path: path, Version: version})
}

// Get returns all image versions for a given Application.
func (s *ImageVersionStore) Get(namespace, appName string) []ImageVersion {
	key := namespace + "/" + appName
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.versions[key]
	if len(entries) == 0 {
		return nil
	}
	// Return a copy to avoid data races
	result := make([]ImageVersion, len(entries))
	copy(result, entries)
	return result
}

// SplitPath splits a dot-separated path string into its components.
func SplitPath(path string) []string {
	return strings.Split(path, ".")
}

// SetHelmValues takes a helm values YAML string, sets the given image versions
// at their respective paths, and returns the updated YAML string.
// This is the shared logic used by both ResourceSubroutine and DeploymentSubroutine.
func SetHelmValues(valuesYAML string, updates []ImageVersion) (string, error) {
	var helmValues map[string]interface{}
	if valuesYAML != "" {
		if err := yaml.Unmarshal([]byte(valuesYAML), &helmValues); err != nil {
			return "", err
		}
	}
	if helmValues == nil {
		helmValues = make(map[string]interface{})
	}

	for _, iv := range updates {
		path := SplitPath(iv.Path)
		current := helmValues
		for i := 0; i < len(path)-1; i++ {
			key := path[i]
			if val, exists := current[key]; exists {
				if valMap, ok := val.(map[string]interface{}); ok {
					current = valMap
				} else {
					newMap := make(map[string]interface{})
					current[key] = newMap
					current = newMap
				}
			} else {
				newMap := make(map[string]interface{})
				current[key] = newMap
				current = newMap
			}
		}
		current[path[len(path)-1]] = iv.Version
	}

	updatedYAML, err := yaml.Marshal(helmValues)
	if err != nil {
		return "", err
	}
	return string(updatedYAML), nil
}
