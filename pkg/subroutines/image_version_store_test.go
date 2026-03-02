package subroutines

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ImageVersionStoreTestSuite struct {
	suite.Suite
}

func TestImageVersionStoreTestSuite(t *testing.T) {
	suite.Run(t, new(ImageVersionStoreTestSuite))
}

func (s *ImageVersionStoreTestSuite) TestNewImageVersionStore() {
	store := NewImageVersionStore()
	s.NotNil(store)
	s.NotNil(store.versions)
	s.Empty(store.versions)
}

func (s *ImageVersionStoreTestSuite) TestSet_NewEntry() {
	store := NewImageVersionStore()

	store.Set("default", "my-app", "image.tag", "v1.0.0")

	result := store.Get("default", "my-app")
	s.Require().Len(result, 1)
	s.Equal("image.tag", result[0].Path)
	s.Equal("v1.0.0", result[0].Version)
}

func (s *ImageVersionStoreTestSuite) TestSet_UpdateExisting() {
	store := NewImageVersionStore()

	store.Set("default", "my-app", "image.tag", "v1.0.0")
	store.Set("default", "my-app", "image.tag", "v2.0.0")

	result := store.Get("default", "my-app")
	s.Require().Len(result, 1)
	s.Equal("image.tag", result[0].Path)
	s.Equal("v2.0.0", result[0].Version)
}

func (s *ImageVersionStoreTestSuite) TestSet_MultiplePaths() {
	store := NewImageVersionStore()

	store.Set("default", "my-app", "image.tag", "v1.0.0")
	store.Set("default", "my-app", "sidecar.image.tag", "v0.5.0")
	store.Set("default", "my-app", "init.image.tag", "v0.1.0")

	result := store.Get("default", "my-app")
	s.Require().Len(result, 3)

	// Verify all paths are present
	paths := make(map[string]string)
	for _, iv := range result {
		paths[iv.Path] = iv.Version
	}
	s.Equal("v1.0.0", paths["image.tag"])
	s.Equal("v0.5.0", paths["sidecar.image.tag"])
	s.Equal("v0.1.0", paths["init.image.tag"])
}

func (s *ImageVersionStoreTestSuite) TestSet_MultipleApps() {
	store := NewImageVersionStore()

	store.Set("ns1", "app1", "image.tag", "v1.0.0")
	store.Set("ns1", "app2", "image.tag", "v2.0.0")
	store.Set("ns2", "app1", "image.tag", "v3.0.0")

	result1 := store.Get("ns1", "app1")
	s.Require().Len(result1, 1)
	s.Equal("v1.0.0", result1[0].Version)

	result2 := store.Get("ns1", "app2")
	s.Require().Len(result2, 1)
	s.Equal("v2.0.0", result2[0].Version)

	result3 := store.Get("ns2", "app1")
	s.Require().Len(result3, 1)
	s.Equal("v3.0.0", result3[0].Version)
}

func (s *ImageVersionStoreTestSuite) TestGet_Empty() {
	store := NewImageVersionStore()

	result := store.Get("default", "nonexistent")
	s.Nil(result)
}

func (s *ImageVersionStoreTestSuite) TestGet_ReturnsCopy() {
	store := NewImageVersionStore()
	store.Set("default", "my-app", "image.tag", "v1.0.0")

	result1 := store.Get("default", "my-app")
	result1[0].Version = "modified"

	result2 := store.Get("default", "my-app")
	s.Equal("v1.0.0", result2[0].Version)
}

func (s *ImageVersionStoreTestSuite) TestConcurrentAccess() {
	store := NewImageVersionStore()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store.Set("default", "my-app", "image.tag", "v1.0.0")
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Get("default", "my-app")
		}()
	}

	wg.Wait()

	result := store.Get("default", "my-app")
	s.Require().Len(result, 1)
}

func (s *ImageVersionStoreTestSuite) TestSplitPath() {
	tests := []struct {
		name     string
		path     string
		expected []string
	}{
		{
			name:     "single element",
			path:     "tag",
			expected: []string{"tag"},
		},
		{
			name:     "two elements",
			path:     "image.tag",
			expected: []string{"image", "tag"},
		},
		{
			name:     "three elements",
			path:     "kcp.image.tag",
			expected: []string{"kcp", "image", "tag"},
		},
		{
			name:     "empty string",
			path:     "",
			expected: []string{""},
		},
		{
			name:     "multiple dots",
			path:     "a.b.c.d.e",
			expected: []string{"a", "b", "c", "d", "e"},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			result := SplitPath(tt.path)
			s.Equal(tt.expected, result)
		})
	}
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_EmptyYAML() {
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
	}

	result, err := SetHelmValues("", updates)
	s.Require().NoError(err)
	s.Contains(result, "image:")
	s.Contains(result, "tag: v1.0.0")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_ExistingYAML() {
	existingYAML := `
replicas: 3
image:
  repository: myrepo
  tag: v0.5.0
`
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
	}

	result, err := SetHelmValues(existingYAML, updates)
	s.Require().NoError(err)
	s.Contains(result, "replicas: 3")
	s.Contains(result, "repository: myrepo")
	s.Contains(result, "tag: v1.0.0")
	s.NotContains(result, "v0.5.0")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_CreateNestedPath() {
	updates := []ImageVersion{
		{Path: "deep.nested.image.tag", Version: "v1.0.0"},
	}

	result, err := SetHelmValues("", updates)
	s.Require().NoError(err)
	s.Contains(result, "deep:")
	s.Contains(result, "nested:")
	s.Contains(result, "image:")
	s.Contains(result, "tag: v1.0.0")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_MultipleUpdates() {
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
		{Path: "sidecar.image.tag", Version: "v0.5.0"},
	}

	result, err := SetHelmValues("", updates)
	s.Require().NoError(err)
	s.Contains(result, "tag: v1.0.0")
	s.Contains(result, "tag: v0.5.0")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_OverwriteNonMapValue() {
	existingYAML := `
image: somestring
`
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
	}

	result, err := SetHelmValues(existingYAML, updates)
	s.Require().NoError(err)
	s.Contains(result, "tag: v1.0.0")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_InvalidYAML() {
	invalidYAML := `
invalid: [
  unclosed bracket
`
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
	}

	_, err := SetHelmValues(invalidYAML, updates)
	s.Error(err)
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_NoUpdates() {
	existingYAML := `
replicas: 3
`
	updates := []ImageVersion{}

	result, err := SetHelmValues(existingYAML, updates)
	s.Require().NoError(err)
	s.Contains(result, "replicas: 3")
}

func (s *ImageVersionStoreTestSuite) TestSetHelmValues_PreservesOtherKeys() {
	existingYAML := `
replicas: 3
image:
  repository: myrepo
  tag: v0.5.0
  pullPolicy: Always
resources:
  limits:
    cpu: 100m
`
	updates := []ImageVersion{
		{Path: "image.tag", Version: "v1.0.0"},
	}

	result, err := SetHelmValues(existingYAML, updates)
	s.Require().NoError(err)
	s.Contains(result, "replicas: 3")
	s.Contains(result, "repository: myrepo")
	s.Contains(result, "pullPolicy: Always")
	s.Contains(result, "cpu: 100m")
	s.Contains(result, "tag: v1.0.0")
}
