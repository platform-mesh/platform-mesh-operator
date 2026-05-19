package rbacpresets

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
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

var allowedManifestKinds = map[string]struct{}{
	"ServiceAccount":     {},
	"ClusterRole":        {},
	"ClusterRoleBinding": {},
	"RoleBinding":        {},
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

func RenderPreset(name string, raw []byte, data PresetTemplateData) (*RenderedPreset, error) {
	if data.Suffix == "" {
		data.Suffix = name
	}
	header, err := renderPresetHeader(name, raw, data)
	if err != nil {
		return nil, err
	}
	if data.ProviderPath == "" {
		data.ProviderPath = header.Spec.ServiceAccountWorkspace
	}
	if data.SAName == "" {
		data.SAName = header.Spec.ServiceAccountName
	}
	if data.SAName == "" {
		data.SAName = "platform-mesh-provider-" + data.Suffix
	}

	renderedBytes, err := executeTemplate(name, raw, data)
	if err != nil {
		return nil, err
	}

	docs, err := parseRenderedDocs(renderedBytes)
	if err != nil {
		return nil, err
	}
	var preset *ProviderRBACPreset
	grouped := map[string][]unstructured.Unstructured{}
	for i := range docs {
		obj := docs[i]
		if obj.GetKind() == "" {
			continue
		}
		if obj.GetAPIVersion() == GroupVersion && obj.GetKind() == KindProviderRBACPreset {
			if preset != nil {
				return nil, fmt.Errorf("preset %q contains multiple %s documents", name, KindProviderRBACPreset)
			}
			var current ProviderRBACPreset
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &current); err != nil {
				return nil, fmt.Errorf("decode %s header in preset %q: %w", KindProviderRBACPreset, name, err)
			}
			preset = &current
			continue
		}
		if _, ok := allowedManifestKinds[obj.GetKind()]; !ok {
			return nil, fmt.Errorf("preset %q contains unsupported manifest kind %q", name, obj.GetKind())
		}
		workspace := strings.TrimSpace(obj.GetAnnotations()[AnnotationWorkspace])
		if workspace == "" && preset != nil {
			workspace = strings.TrimSpace(preset.Spec.ServiceAccountWorkspace)
		}
		if workspace == "" {
			workspace = strings.TrimSpace(header.Spec.ServiceAccountWorkspace)
		}
		if workspace == "" {
			return nil, fmt.Errorf("preset %q manifest %s/%s has no %s annotation and no serviceAccountWorkspace default", name, obj.GetKind(), obj.GetName(), AnnotationWorkspace)
		}
		stripWorkspaceAnnotation(&obj)
		grouped[workspace] = append(grouped[workspace], obj)
	}
	if preset == nil {
		return nil, fmt.Errorf("preset %q missing %s header document", name, KindProviderRBACPreset)
	}
	if preset.Spec.ServiceAccountWorkspace == "" {
		preset.Spec.ServiceAccountWorkspace = header.Spec.ServiceAccountWorkspace
	}
	if preset.Spec.ServiceAccountWorkspace == "" {
		preset.Spec.ServiceAccountWorkspace = data.ProviderPath
	}
	if preset.Spec.ServiceAccountName == "" {
		preset.Spec.ServiceAccountName = data.SAName
	}

	workspaces := make([]string, 0, len(grouped))
	for workspace := range grouped {
		workspaces = append(workspaces, workspace)
	}
	sort.Strings(workspaces)
	byWorkspace := make([]WorkspaceManifests, 0, len(workspaces))
	for _, workspace := range workspaces {
		byWorkspace = append(byWorkspace, WorkspaceManifests{
			Workspace: workspace,
			Manifests: grouped[workspace],
		})
	}
	return &RenderedPreset{
		Spec:        preset.Spec,
		ByWorkspace: byWorkspace,
	}, nil
}

func renderPresetHeader(name string, raw []byte, data PresetTemplateData) (*ProviderRBACPreset, error) {
	renderedBytes, err := executeTemplate(name+"-header", raw, data)
	if err != nil {
		return nil, err
	}
	docs, err := parseRenderedDocs(renderedBytes)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		obj := docs[i]
		if obj.GetAPIVersion() == GroupVersion && obj.GetKind() == KindProviderRBACPreset {
			var preset ProviderRBACPreset
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &preset); err != nil {
				return nil, fmt.Errorf("decode %s header in preset %q: %w", KindProviderRBACPreset, name, err)
			}
			if preset.Spec.ServiceAccountWorkspace == "" {
				preset.Spec.ServiceAccountWorkspace = data.ProviderPath
			}
			return &preset, nil
		}
	}
	return nil, fmt.Errorf("preset %q missing %s header document", name, KindProviderRBACPreset)
}

func executeTemplate(name string, raw []byte, data PresetTemplateData) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse preset template %q: %w", name, err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("execute preset template %q: %w", name, err)
	}
	return rendered.Bytes(), nil
}

func parseRenderedDocs(rendered []byte) ([]unstructured.Unstructured, error) {
	rawDocs := strings.Split(string(rendered), "\n---")
	docs := make([]unstructured.Unstructured, 0, len(rawDocs))
	for _, rawDoc := range rawDocs {
		rawDoc = strings.TrimSpace(rawDoc)
		if rawDoc == "" {
			continue
		}
		var objMap map[string]interface{}
		if err := yaml.Unmarshal([]byte(rawDoc), &objMap); err != nil {
			return nil, fmt.Errorf("unmarshal preset manifest: %w", err)
		}
		if len(objMap) == 0 {
			continue
		}
		docs = append(docs, unstructured.Unstructured{Object: objMap})
	}
	return docs, nil
}

func stripWorkspaceAnnotation(obj *unstructured.Unstructured) {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return
	}
	delete(annotations, AnnotationWorkspace)
	if len(annotations) == 0 {
		obj.SetAnnotations(nil)
		return
	}
	obj.SetAnnotations(annotations)
}
