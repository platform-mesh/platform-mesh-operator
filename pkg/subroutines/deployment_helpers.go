package subroutines

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/platform-mesh/platform-mesh-operator/pkg/merge"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	// Field manager names for Server-Side Apply
	fieldManagerDeployment = "platform-mesh-deployment"
	fieldManagerResource   = "platform-mesh-resource"
	fieldManagerOperator   = "platform-mesh-operator"

	// Error patterns for detecting SSA failures
	errorPatternSchemaValidation = "field not declared in schema"
	errorPatternTypedPatch       = "failed to create typed patch object"
	errorPatternConflict         = "conflict with"
	errorPatternApplyFailed      = "Apply failed with"

	// Kubernetes resource kind names
	kindHelmRelease = "HelmRelease"
	kindResource    = "Resource"

	// Spec field names
	specFieldValues   = "values"
	specFieldChart    = "chart"
	specFieldInterval = "interval"
)

// dynamicClientProvider provides dynamic client infrastructure for Server-Side Apply.
type dynamicClientProvider struct {
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	mapper          meta.RESTMapper
}

// newDynamicClientProvider creates a new dynamic client provider from REST config.
// restConfig must not be nil - it should be set via SetRestConfig() from the manager.
func newDynamicClientProvider(restConfig *rest.Config) (*dynamicClientProvider, error) {
	if restConfig == nil {
		return nil, errors.New("REST config is nil - SetRestConfig() must be called before using dynamic client")
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create dynamic client")
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create discovery client")
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	return &dynamicClientProvider{
		dynamicClient:   dynamicClient,
		discoveryClient: discoveryClient,
		mapper:          mapper,
	}, nil
}

// getResourceInterface returns the appropriate ResourceInterface for the given object.
func (p *dynamicClientProvider) getResourceInterface(obj *unstructured.Unstructured) (dynamic.ResourceInterface, error) {
	gvk := obj.GroupVersionKind()
	mapping, err := p.mapper.RESTMapping(schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}, gvk.Version)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get REST mapping for %s", gvk.String())
	}

	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		return p.dynamicClient.Resource(mapping.Resource).Namespace(ns), nil
	}

	return p.dynamicClient.Resource(mapping.Resource), nil
}

// isSSAError indicates if an error is a schema validation or conflict error that should trigger fallback.
func isSSAError(err error) (isSchemaError, isConflictError bool) {
	if err == nil {
		return false, false
	}

	errStr := err.Error()
	isSchemaError = strings.Contains(errStr, errorPatternSchemaValidation) ||
		strings.Contains(errStr, errorPatternTypedPatch)
	isConflictError = strings.Contains(errStr, errorPatternConflict) ||
		(strings.Contains(errStr, errorPatternApplyFailed) && strings.Contains(errStr, "conflict"))

	return isSchemaError, isConflictError
}

// applyWithSSA attempts to apply an object using Server-Side Apply.
func applyWithSSA(ctx context.Context, provider *dynamicClientProvider, obj *unstructured.Unstructured, fieldManager string) error {
	ri, err := provider.getResourceInterface(obj)
	if err != nil {
		return err
	}

	data, err := json.Marshal(obj.Object)
	if err != nil {
		return errors.Wrap(err, "Failed to marshal object to JSON")
	}

	force := false
	po := metav1.PatchOptions{
		FieldManager: fieldManager,
		Force:        &force,
	}

	_, err = ri.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, po)
	return err
}

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
		if err := r.applyWithDynamicClient(ctx, obj, k8sClient, fieldManagerDeployment, log); err != nil {
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
