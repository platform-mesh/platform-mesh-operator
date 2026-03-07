package subroutines

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

var argoApplicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

const (
	// Field manager names for Server-Side Apply
	fieldManagerDeployment = "platform-mesh-deployment"
)

// updateObjectMetadata updates labels and annotations from desired to existing.
func updateObjectMetadata(existing, desired *unstructured.Unstructured) {
	if labels := desired.GetLabels(); labels != nil {
		existing.SetLabels(labels)
	}
	if annotations := desired.GetAnnotations(); annotations != nil {
		existing.SetAnnotations(annotations)
	}
}

// renderAndApplyTemplates is a generic function to render and apply templates from a directory.
func (r *DeploymentSubroutine) renderAndApplyTemplates(
	ctx context.Context,
	dir string,
	tmplVars map[string]interface{},
	k8sClient client.Client,
	log *logger.Logger,
	templateType string,
) errors.OperatorError {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}

		// Read and render template
		obj, err := r.renderTemplateFile(path, tmplVars, log)
		if err != nil {
			return errors.Wrap(err, "Failed to render template: %s", path)
		}

		if obj == nil {
			// Template rendered empty, skip
			return nil
		}

		// Apply the rendered manifest
		if err := k8sClient.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManagerDeployment), client.ForceOwnership); err != nil {
			return errors.Wrap(err, "Failed to apply rendered manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
		}

		return nil
	})

	if err != nil {
		log.Error().Err(err).Str("type", templateType).Msg("Failed to render and apply templates")
		return errors.NewOperatorError(err, false, true)
	}

	return nil
}

// renderTemplateFile reads a template file, renders it, and returns an unstructured object.
// Returns nil if the template renders empty.
func (r *DeploymentSubroutine) renderTemplateFile(path string, tmplVars map[string]interface{}, log *logger.Logger) (*unstructured.Unstructured, error) {
	templateBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to read template file")
	}

	tmpl, err := template.New(filepath.Base(path)).Funcs(templateFuncMap()).Parse(string(templateBytes))
	if err != nil {
		return nil, errors.Wrap(err, "Failed to parse template")
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, tmplVars); err != nil {
		return nil, errors.Wrap(err, "Failed to execute template")
	}

	renderedStr := strings.TrimSpace(rendered.String())
	if renderedStr == "" {
		log.Debug().Str("path", path).Msg("Template rendered empty, skipping")
		return nil, nil
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal(rendered.Bytes(), &objMap); err != nil {
		return nil, errors.Wrap(err, "Failed to unmarshal rendered YAML (size: %d bytes)", len(rendered.Bytes()))
	}

	return &unstructured.Unstructured{Object: objMap}, nil
}

// helper: functions for Helm-like templates in components gotemplates
func isZeroValue(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return rv.IsZero()
}

func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"default": func(d, v interface{}) interface{} {
			if isZeroValue(v) {
				return d
			}
			return v
		},
		"toYaml": func(v interface{}) (string, error) {
			b, err := yaml.Marshal(v)
			return string(b), err
		},
		"nindent": func(spaces int, s string) string {
			if s == "" {
				return ""
			}
			pad := strings.Repeat(" ", spaces)
			lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
			// Filter out empty lines at the start
			startIdx := 0
			for startIdx < len(lines) && strings.TrimSpace(lines[startIdx]) == "" {
				startIdx++
			}
			if startIdx >= len(lines) {
				return ""
			}
			// Filter out empty lines at the end
			endIdx := len(lines)
			for endIdx > startIdx && strings.TrimSpace(lines[endIdx-1]) == "" {
				endIdx--
			}
			// Indent non-empty lines
			for i := startIdx; i < endIdx; i++ {
				if strings.TrimSpace(lines[i]) != "" {
					lines[i] = pad + lines[i]
				}
			}
			result := strings.Join(lines[startIdx:endIdx], "\n")
			if result != "" {
				result += "\n"
			}
			return result
		},
		"or": func(a, b interface{}) interface{} {
			if !isZeroValue(a) {
				return a
			}
			return b
		},
		"and": func(a, b interface{}) bool {
			return !isZeroValue(a) && !isZeroValue(b)
		},
		"not": func(v interface{}) bool {
			return isZeroValue(v)
		},
	}
}

// preserveExistingArgoSourceFields checks if an existing ArgoCD Application has repoURL/targetRevision
// values set by ResourceSubroutine and removes those fields from the new object to preserve them.
// This prevents DeploymentSubroutine from overwriting values managed by ResourceSubroutine.
func (r *DeploymentSubroutine) preserveExistingArgoSourceFields(
	ctx context.Context,
	objMap map[string]interface{},
	name, namespace string,
	log *logger.Logger,
) {
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(argoApplicationGVK)

	if err := r.clientInfra.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, existingApp); err != nil {
		// Application doesn't exist yet, nothing to preserve
		return
	}

	// Application exists - check if repoURL and targetRevision are already set (not placeholders)
	existingRepoURL, found, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "repoURL")
	existingTargetRevision, foundRev, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "targetRevision")

	// Check if the new object has repoURL/targetRevision before trying to preserve
	var newRepoURL, newTargetRevision string
	if spec, ok := objMap["spec"].(map[string]interface{}); ok {
		if source, ok := spec["source"].(map[string]interface{}); ok {
			if url, ok := source["repoURL"].(string); ok {
				newRepoURL = url
			}
			if rev, ok := source["targetRevision"].(string); ok {
				newTargetRevision = rev
			}
		}
	}

	// Only preserve if:
	// 1. Existing value is set and not a placeholder
	// 2. New object has the field (so we don't remove required fields)
	// 3. Existing value is different from new value (to avoid unnecessary removals)
	if found && existingRepoURL != "" && existingRepoURL != argoPlaceholderRepoURL && newRepoURL != "" && existingRepoURL != newRepoURL {
		if spec, ok := objMap["spec"].(map[string]interface{}); ok {
			if source, ok := spec["source"].(map[string]interface{}); ok {
				delete(source, "repoURL")
				log.Debug().Str("app", name).Msg("Preserving existing repoURL from ResourceSubroutine")
			}
		}
	}
	if foundRev && existingTargetRevision != "" && existingTargetRevision != argoPlaceholderRepoURL && newTargetRevision != "" && existingTargetRevision != newTargetRevision {
		if spec, ok := objMap["spec"].(map[string]interface{}); ok {
			if source, ok := spec["source"].(map[string]interface{}); ok {
				delete(source, "targetRevision")
				log.Debug().Str("app", name).Msg("Preserving existing targetRevision from ResourceSubroutine")
			}
		}
	}
}
