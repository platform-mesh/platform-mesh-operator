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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/platform-mesh/platform-mesh-operator/pkg/merge"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	// Kubernetes resource kind names
	kindHelmRelease = "HelmRelease"
	kindResource    = "Resource"

	// Spec field names
	specFieldValues   = "values"
	specFieldChart    = "chart"
	specFieldInterval = "interval"
)

// mergeHelmReleaseSpec merges HelmRelease spec fields, preserving existing values and chart managed by Resource subroutine.
func mergeHelmReleaseSpec(existing, desired *unstructured.Unstructured, log *logger.Logger) error {
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")

	if desiredSpec == nil {
		return nil
	}

	if existingSpec == nil {
		return unstructured.SetNestedField(existing.Object, desiredSpec, "spec")
	}

	// Merge values field
	if err := mergeHelmReleaseField(existing, existingSpec, desiredSpec, specFieldValues, log); err != nil {
		return err
	}

	// Merge chart field
	if err := mergeHelmReleaseField(existing, existingSpec, desiredSpec, specFieldChart, log); err != nil {
		return err
	}

	// Merge other top-level spec fields (desired takes precedence)
	for k, v := range desiredSpec {
		if k != specFieldValues && k != specFieldChart {
			if err := unstructured.SetNestedField(existing.Object, v, "spec", k); err != nil {
				return errors.Wrap(err, "Failed to set spec field %s", k)
			}
		}
	}

	return nil
}

// mergeHelmReleaseField merges a specific field in HelmRelease spec.
func mergeHelmReleaseField(existing *unstructured.Unstructured, existingSpec, desiredSpec map[string]interface{}, fieldName string, log *logger.Logger) error {
	existingField, existingHasField := existingSpec[fieldName].(map[string]interface{})
	desiredField, desiredHasField := desiredSpec[fieldName].(map[string]interface{})

	switch {
	case existingHasField && desiredHasField:
		// Both exist, merge them (existing takes precedence)
		merged, mergeErr := merge.MergeMaps(desiredField, existingField, log)
		if mergeErr != nil {
			log.Debug().Err(mergeErr).Str("field", fieldName).Msg("Failed to merge HelmRelease field, using desired")
			merged = desiredField
		}
		return unstructured.SetNestedField(existing.Object, merged, "spec", fieldName)

	case desiredHasField:
		// Only desired has it, use desired
		return unstructured.SetNestedField(existing.Object, desiredField, "spec", fieldName)

	default:
		// Neither has it or only existing has it, keep existing (no-op)
		return nil
	}
}

// mergeResourceSpec merges Resource spec, excluding interval which is managed by the OCM controller.
func mergeResourceSpec(existing, desired *unstructured.Unstructured, log *logger.Logger) error {
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")

	if desiredSpec == nil {
		return nil
	}

	if existingSpec == nil {
		// Remove interval from desired spec before creating
		if _, hasInterval := desiredSpec[specFieldInterval]; hasInterval {
			desiredSpecCopy := make(map[string]interface{})
			for k, v := range desiredSpec {
				if k != specFieldInterval {
					desiredSpecCopy[k] = v
				}
			}
			desiredSpec = desiredSpecCopy
		}
		return unstructured.SetNestedField(existing.Object, desiredSpec, "spec")
	}

	// Remove interval from desired spec before merging (OCM controller manages it)
	desiredSpecCopy := make(map[string]interface{})
	for k, v := range desiredSpec {
		if k != specFieldInterval {
			desiredSpecCopy[k] = v
		}
	}

	// Merge entire spec (existing takes precedence, preserving interval from OCM controller)
	mergedSpec, mergeErr := merge.MergeMaps(desiredSpecCopy, existingSpec, log)
	if mergeErr != nil {
		log.Debug().Err(mergeErr).Msg("Failed to merge Resource spec, using desired spec")
		mergedSpec = desiredSpecCopy
	}

	return unstructured.SetNestedField(existing.Object, mergedSpec, "spec")
}

// mergeGenericSpec merges spec for non-HelmRelease, non-Resource resources.
func mergeGenericSpec(existing, desired *unstructured.Unstructured, log *logger.Logger) error {
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")

	if desiredSpec == nil {
		return nil
	}

	if existingSpec == nil {
		return unstructured.SetNestedField(existing.Object, desiredSpec, "spec")
	}

	// Merge entire spec (existing takes precedence)
	mergedSpec, mergeErr := merge.MergeMaps(desiredSpec, existingSpec, log)
	if mergeErr != nil {
		log.Debug().Err(mergeErr).Msg("Failed to merge spec, using desired spec")
		mergedSpec = desiredSpec
	}

	return unstructured.SetNestedField(existing.Object, mergedSpec, "spec")
}

// updateObjectMetadata updates labels and annotations from desired to existing.
func updateObjectMetadata(existing, desired *unstructured.Unstructured) {
	if labels := desired.GetLabels(); labels != nil {
		existing.SetLabels(labels)
	}
	if annotations := desired.GetAnnotations(); annotations != nil {
		existing.SetAnnotations(annotations)
	}
}

// getOrCreateObject retrieves an existing object or creates it if not found.
func getOrCreateObject(ctx context.Context, k8sClient client.Client, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	existing.SetName(obj.GetName())
	existing.SetNamespace(obj.GetNamespace())

	err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if err == nil {
		return existing, nil
	}

	// If not found, create it
	if kerrors.IsNotFound(err) {
		if createErr := k8sClient.Create(ctx, obj); createErr != nil {
			return nil, errors.Wrap(createErr, "Failed to create object")
		}
		return obj, nil
	}

	return nil, errors.Wrap(err, "Failed to get existing object")
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

		log.Debug().Str("path", path).Str("type", templateType).Msg("Rendering template")

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
		if err := r.applyWithUpdate(ctx, obj, k8sClient, log); err != nil {
			return errors.Wrap(err, "Failed to apply rendered manifest from template: %s (%s/%s)", path, obj.GetKind(), obj.GetName())
		}

		log.Debug().Str("path", path).Str("kind", obj.GetKind()).Str("name", obj.GetName()).Msg("Applied rendered template")
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

	tmpl, err := template.New(filepath.Base(path)).Parse(string(templateBytes))
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
		return nil, errors.Wrap(err, "Failed to unmarshal rendered YAML. Output:\n%s", renderedStr)
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
