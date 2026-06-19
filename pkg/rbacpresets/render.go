package rbacpresets

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type PresetTemplateData struct {
	ProviderPath string
	RawPath      string
	SAName       string
	Suffix       string
}

type RenderedPreset struct {
	Spec        ProviderRBACPresetSpec
	ByWorkspace []WorkspaceManifests
}

type WorkspaceManifests struct {
	Workspace string
	Manifests []unstructured.Unstructured
}

func RenderPreset(name string, raw []byte, data PresetTemplateData) (*RenderedPreset, error) {
	if data.Suffix == "" {
		data.Suffix = name
	}

	renderedBytes, err := executeTemplate(name, raw, data)
	if err != nil {
		return nil, err
	}

	headerSpec, err := extractHeaderSpec(name, renderedBytes, data.ProviderPath)
	if err != nil {
		return nil, err
	}

	resolved := resolveTemplateData(data, headerSpec)
	if templateDataChanged(data, resolved) {
		renderedBytes, err = executeTemplate(name, raw, resolved)
		if err != nil {
			return nil, err
		}
	}
	data = resolved

	return groupRenderedPreset(name, renderedBytes, data, headerSpec)
}

func resolveTemplateData(data PresetTemplateData, headerSpec ProviderRBACPresetSpec) PresetTemplateData {
	resolved := data
	if resolved.ProviderPath == "" {
		resolved.ProviderPath = headerSpec.ServiceAccountWorkspace
	}
	if resolved.SAName == "" {
		resolved.SAName = headerSpec.ServiceAccountName
	}
	if resolved.SAName == "" {
		resolved.SAName = "platform-mesh-provider-" + resolved.Suffix
	}
	return resolved
}

func templateDataChanged(before, after PresetTemplateData) bool {
	return before.ProviderPath != after.ProviderPath || before.SAName != after.SAName
}

func extractHeaderSpec(name string, rendered []byte, providerPath string) (ProviderRBACPresetSpec, error) {
	for _, rawDoc := range splitRenderedDocs(rendered) {
		if !isPresetHeaderDoc(rawDoc) {
			continue
		}
		spec, err := decodePresetSpec(rawDoc)
		if err != nil {
			return ProviderRBACPresetSpec{}, fmt.Errorf("decode %s header in preset %q: %w", KindProviderRBACPreset, name, err)
		}
		if spec.ServiceAccountWorkspace == "" {
			spec.ServiceAccountWorkspace = providerPath
		}
		return spec, nil
	}
	return ProviderRBACPresetSpec{}, fmt.Errorf("preset %q missing %s header document", name, KindProviderRBACPreset)
}

func groupRenderedPreset(name string, renderedBytes []byte, data PresetTemplateData, headerSpec ProviderRBACPresetSpec) (*RenderedPreset, error) {
	rawDocs := splitRenderedDocs(renderedBytes)
	var presetSpec *ProviderRBACPresetSpec
	grouped := map[string][]unstructured.Unstructured{}
	for _, rawDoc := range rawDocs {
		if isPresetHeaderDoc(rawDoc) {
			if presetSpec != nil {
				return nil, fmt.Errorf("preset %q contains multiple %s documents", name, KindProviderRBACPreset)
			}
			spec, err := decodePresetSpec(rawDoc)
			if err != nil {
				return nil, fmt.Errorf("decode %s header in preset %q: %w", KindProviderRBACPreset, name, err)
			}
			presetSpec = &spec
			continue
		}
		obj, err := parseManifestDoc(rawDoc)
		if err != nil {
			return nil, err
		}
		if obj.GetKind() == "" {
			continue
		}
		if _, ok := allowedManifestKinds[obj.GetKind()]; !ok {
			return nil, fmt.Errorf("preset %q contains unsupported manifest kind %q", name, obj.GetKind())
		}
		workspace := strings.TrimSpace(obj.GetAnnotations()[AnnotationWorkspace])
		if workspace == "" && presetSpec != nil {
			workspace = strings.TrimSpace(presetSpec.ServiceAccountWorkspace)
		}
		if workspace == "" {
			workspace = strings.TrimSpace(headerSpec.ServiceAccountWorkspace)
		}
		if workspace == "" {
			return nil, fmt.Errorf("preset %q manifest %s/%s has no %s annotation and no serviceAccountWorkspace default", name, obj.GetKind(), obj.GetName(), AnnotationWorkspace)
		}
		stripWorkspaceAnnotation(&obj)
		grouped[workspace] = append(grouped[workspace], obj)
	}
	if presetSpec == nil {
		return nil, fmt.Errorf("preset %q missing %s header document", name, KindProviderRBACPreset)
	}
	if presetSpec.ServiceAccountWorkspace == "" {
		presetSpec.ServiceAccountWorkspace = headerSpec.ServiceAccountWorkspace
	}
	if presetSpec.ServiceAccountWorkspace == "" {
		presetSpec.ServiceAccountWorkspace = data.ProviderPath
	}
	if presetSpec.ServiceAccountName == "" {
		presetSpec.ServiceAccountName = data.SAName
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
		Spec:        *presetSpec,
		ByWorkspace: byWorkspace,
	}, nil
}

func decodePresetSpec(rawDoc []byte) (ProviderRBACPresetSpec, error) {
	var doc presetDocument
	if err := yaml.Unmarshal(rawDoc, &doc); err != nil {
		return ProviderRBACPresetSpec{}, err
	}
	return doc.Spec, nil
}

func isPresetHeaderDoc(rawDoc []byte) bool {
	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(rawDoc, &header); err != nil {
		return false
	}
	return header.Kind == KindProviderRBACPreset
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

func splitRenderedDocs(rendered []byte) [][]byte {
	rawDocs := strings.Split(string(rendered), "\n---")
	docs := make([][]byte, 0, len(rawDocs))
	for _, rawDoc := range rawDocs {
		rawDoc = strings.TrimSpace(rawDoc)
		if rawDoc == "" {
			continue
		}
		docs = append(docs, []byte(rawDoc))
	}
	return docs
}

func parseManifestDoc(rawDoc []byte) (unstructured.Unstructured, error) {
	var objMap map[string]interface{}
	if err := yaml.Unmarshal(rawDoc, &objMap); err != nil {
		return unstructured.Unstructured{}, fmt.Errorf("unmarshal preset manifest: %w", err)
	}
	if len(objMap) == 0 {
		return unstructured.Unstructured{}, nil
	}
	return unstructured.Unstructured{Object: objMap}, nil
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
