package rbacpresets

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed providers/*.yaml
var providerPresetFilesEmbedded embed.FS

// EmbeddedProvidersFS returns the embedded production preset files (providers/*.yaml).
func EmbeddedProvidersFS() fs.FS {
	return providerPresetFilesEmbedded
}

// Loader reads provider RBAC preset YAML from an io/fs.FS (expected layout: providers/<name>.yaml).
type Loader struct {
	FS fs.FS
}

// NewLoader returns a Loader that reads presets from f. f must not be nil.
func NewLoader(f fs.FS) *Loader {
	if f == nil {
		panic("rbacpresets: NewLoader: fs.FS is nil")
	}
	return &Loader{FS: f}
}

// MergePresetFS returns an fs.FS that resolves paths from overlay first, then base.
func MergePresetFS(base, overlay fs.FS) fs.FS {
	return mergedPresetFS{base: base, overlay: overlay}
}

type mergedPresetFS struct {
	base, overlay fs.FS
}

func (m mergedPresetFS) Open(name string) (fs.File, error) {
	f, err := m.overlay.Open(name)
	if err == nil {
		return f, nil
	}
	return m.base.Open(name)
}

// LoadPreset reads providers/<name>.yaml from l.FS and renders the preset.
func (l *Loader) LoadPreset(name string, data PresetTemplateData) (*RenderedPreset, error) {
	presetName := strings.TrimSpace(name)
	if presetName == "" {
		return nil, fmt.Errorf("preset name is empty")
	}
	if strings.Contains(presetName, "/") || strings.Contains(presetName, "\\") || strings.Contains(presetName, "..") {
		return nil, fmt.Errorf("invalid preset name %q", name)
	}
	raw, err := fs.ReadFile(l.FS, filepath.ToSlash(filepath.Join("providers", presetName+".yaml")))
	if err != nil {
		return nil, fmt.Errorf("load provider RBAC preset %q: %w", presetName, err)
	}
	return RenderPreset(presetName, raw, data)
}
