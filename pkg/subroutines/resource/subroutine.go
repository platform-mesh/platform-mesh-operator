package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/ocm"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

var ociRepoGvk = schema.GroupVersionKind{
	Group:   "source.toolkit.fluxcd.io",
	Version: "v1",
	Kind:    "OCIRepository",
}

var gitRepoGvk = schema.GroupVersionKind{
	Group:   "source.toolkit.fluxcd.io",
	Version: "v1",
	Kind:    "GitRepository",
}

var helmRepoGvk = schema.GroupVersionKind{
	Group:   "source.toolkit.fluxcd.io",
	Version: "v1",
	Kind:    "HelmRepository",
}

var helmReleaseGvk = schema.GroupVersionKind{
	Group:   "helm.toolkit.fluxcd.io",
	Version: "v2",
	Kind:    "HelmRelease",
}

var argocdApplicationGvk = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

var resourceFieldManager = "platform-mesh-resource"

type ResourceSubroutine struct {
	client        client.Client // infra client for creating FluxCD resources
	clientRuntime client.Client // runtime client for reading profile ConfigMaps
	cfg           *config.OperatorConfig
}

func NewResourceSubroutine(client client.Client, cfg *config.OperatorConfig) *ResourceSubroutine {
	return &ResourceSubroutine{client: client, clientRuntime: client, cfg: cfg}
}

// SetRuntimeClient sets the runtime client for reading profile ConfigMaps
// This should be called after creation if a different client is needed for the runtime cluster
func (r *ResourceSubroutine) SetRuntimeClient(clientRuntime client.Client) {
	r.clientRuntime = clientRuntime
}

func (r *ResourceSubroutine) GetName() string {
	return "ResourceSubroutine"
}

func (r *ResourceSubroutine) Finalize(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{}
}

func getAnnotations(obj *unstructured.Unstructured) map[string]string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	return annotations
}

// getMetadataValue retrieves a value from annotations first, then falls back to labels for backwards compatibility
func getMetadataValue(obj *unstructured.Unstructured, key string) string {
	annotations := getAnnotations(obj)
	if value, ok := annotations[key]; ok && value != "" {
		return value
	}

	labels := obj.GetLabels()
	if labels != nil {
		return labels[key]
	}

	return ""
}

func (r *ResourceSubroutine) Process(ctx context.Context, runtimeObj runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	inst := runtimeObj.(*unstructured.Unstructured)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("name", r.GetName())

	// Get deploymentTechnology from profile (defaults to fluxcd if not found)
	deploymentTech := r.getDeploymentTechnologyFromProfile(ctx, inst.GetNamespace(), log)
	deploymentTech = strings.ToLower(deploymentTech)
	log.Info().Str("deploymentTechnology", deploymentTech).Str("resource", inst.GetName()).Str("namespace", inst.GetNamespace()).Msg("Checking deployment technology for Resource")

	repo := getMetadataValue(inst, "repo")
	artifact := getMetadataValue(inst, "artifact")

	if deploymentTech == "argocd" {
		log.Info().Str("deploymentTechnology", deploymentTech).Str("resource", inst.GetName()).Msg("ArgoCD is enabled, updating ArgoCD Application")
		// For ArgoCD, update the Application based on artifact type
		if artifact == "chart" {
			log.Debug().Msg("Update ArgoCD Application targetRevision and repoURL for chart artifact")
			result, err := r.updateArgoCDApplication(ctx, inst, log)
			if err != nil {
				return result, err
			}
		} else if artifact == "image" {
			log.Debug().Msg("Update ArgoCD Application Helm values (image.tag) for image artifact")
			result, err := r.updateArgoCDApplicationHelmValues(ctx, inst, log)
			if err != nil {
				return result, err
			}
		} else {
			log.Info().Str("artifact", artifact).Msg("ArgoCD is enabled but artifact is not 'chart' or 'image', skipping")
		}
		return ctrl.Result{}, nil
	}

	log.Info().Str("deploymentTechnology", deploymentTech).Str("resource", inst.GetName()).Msg("FluxCD is enabled, proceeding with FluxCD resource creation")

	if repo == "oci" && artifact == "chart" {
		log.Debug().Msg("Create/Update OCI Repo")
		result, err := r.updateOciRepo(ctx, inst, log)
		if err != nil {
			return result, err
		}
	}
	if repo == "git" && artifact == "chart" {
		log.Debug().Msg("Create/Update Git Repo")
		result, err := r.updateGitRepo(ctx, inst, log)
		if err != nil {
			return result, err
		}
	}
	if repo == "helm" && artifact == "chart" {
		log.Debug().Msg("Create/Update Flux Helm Repository Repo")
		result, err := r.updateHelmRepository(ctx, inst, log)
		if err != nil {
			return result, err
		}
		log.Debug().Msg("Update Flux Helm Release Repo")
		result, err = r.updateHelmRelease(ctx, inst, log)
		if err != nil {
			return result, err
		}
	}
	if (repo == "helm" && artifact == "image") || (repo == "oci" && artifact == "image") {
		log.Debug().Msg("Update Helm Release with Image Tag")
		result, err := r.updateHelmReleaseWithImageTag(ctx, inst, log)
		if err != nil {
			return result, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateHelmReleaseWithImageTag(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmReleaseGvk)

	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	forVal := getMetadataValue(inst, "for")
	log.Info().Msgf("Update Helm Release with Image Tag: %s", forVal)
	if forVal != "" {
		forValElems := strings.Split(forVal, "/")
		if len(forValElems) == 2 {
			obj.SetNamespace(forValElems[0])
			obj.SetName(forValElems[1])
		} else {
			obj.SetName(forVal)
		}
	}

	pathLabelStr := getMetadataValue(inst, "path")
	updatePath := []string{"spec", "values", "image", "tag"}
	if pathLabelStr != "" {
		pathElems := strings.Split(pathLabelStr, ".")
		updatePath = []string{"spec", "values"}
		updatePath = append(updatePath, pathElems...)
	}

	versionPathStr := getMetadataValue(inst, "version-path")
	versionPath := []string{"status", "resource", "version"}
	if versionPathStr != "" {
		versionPathElems := strings.Split(versionPathStr, ".")
		versionPath = []string{}
		versionPath = append(versionPath, versionPathElems...)
	}

	version, found, err := unstructured.NestedString(inst.Object, versionPath...)
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
	}

	// Create a minimal patch object with only the field we're updating
	// This ensures Server-Side Apply only tracks ownership of this specific field
	// We don't need to Get the existing object since we already have name/namespace
	// from the 'for' annotation or the Resource instance itself
	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(helmReleaseGvk)
	patchObj.SetName(obj.GetName())
	patchObj.SetNamespace(obj.GetNamespace())

	// Set only the field we're managing (the version at the specified path)
	if err = unstructured.SetNestedField(patchObj.Object, version, updatePath...); err != nil {
		log.Error().Err(err).Msg("Failed to set version in HelmRelease spec")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	// Use Server-Side Apply with field manager to update only the specific field
	// This allows Kubernetes to merge with fields managed by other subroutines (e.g., Deployment subroutine)
	err = r.client.Patch(ctx, patchObj, client.Apply,
		client.FieldOwner(resourceFieldManager),
		client.ForceOwnership)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateArgoCDApplication(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(argocdApplicationGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	// Determine resource type and get repoURL and targetRevision accordingly
	// Check in order: helmRepository (Helm charts), repoUrl (Git charts), imageReference (OCI charts)
	var repoURL string
	var targetRevision string
	isGitChart := false

	helmRepo, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "helmRepository")
	if err == nil && found && helmRepo != "" {
		// For Helm chart resources, use helmRepository from access
		repoURL = helmRepo
		// For Helm charts, use version from status.resource.version
		version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
		if err != nil || !found {
			log.Info().Err(err).Msg("Failed to get version from Resource status")
			return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not found in Resource status"), true, false)
		}
		targetRevision = version
		log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "helmChart").Msg("Using helmRepository for repoURL")
	} else {
		// Check for Git-based chart resources
		gitRepoUrl, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "repoUrl")
		if err == nil && found && gitRepoUrl != "" {
			// For Git chart resources, use repoUrl directly from access
			repoURL = gitRepoUrl
			isGitChart = true
			// For Git charts, prefer ref (Git tag/branch) over resource version
			// Resource version might be a chart version (e.g., 0.1.0) while ref is the Git tag (e.g., v0.31.0)
			gitRef, foundRef, errRef := unstructured.NestedString(inst.Object, "status", "resource", "access", "ref")
			if errRef == nil && foundRef && gitRef != "" {
				targetRevision = gitRef
				log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "gitChart").Msg("Using repoUrl and ref for Git chart")
			} else {
				// Fallback to component version if ref is not available
				componentVersion, found, err := unstructured.NestedString(inst.Object, "status", "component", "version")
				if err == nil && found && componentVersion != "" {
					targetRevision = componentVersion
					log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "gitChart").Msg("Using repoUrl and component version for Git chart")
				} else {
					// Fallback to resource version if component version is not available
					version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
					if err == nil && found && version != "" {
						targetRevision = version
						log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "gitChart").Msg("Using repoUrl and resource version for Git chart")
					} else {
						// Final fallback to commit if nothing else is available
						gitCommit, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "commit")
						if err == nil && found && gitCommit != "" {
							targetRevision = gitCommit
							log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "gitChart").Msg("Using repoUrl and commit for Git chart")
						} else {
							log.Info().Err(err).Msg("Failed to get ref, version, or commit from Resource status for Git chart")
							return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("neither ref, version, nor commit found in Resource status for Git chart"), true, false)
						}
					}
				}
			}
		} else {
			// For OCI resources, extract repoURL from imageReference
			imageReference, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "imageReference")
			if err != nil || !found || imageReference == "" {
				// Try alternative path
				imageReference, found, err = unstructured.NestedString(inst.Object, "status", "resource", "imageReference")
				if err != nil || !found || imageReference == "" {
					log.Info().Err(err).Msg("Failed to get helmRepository, repoUrl, or imageReference from Resource status")
					return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("neither helmRepository, repoUrl, nor imageReference found in Resource status"), true, false)
				}
			}

			// Extract repoURL from imageReference
			// imageReference format: ghcr.io/platform-mesh/helm-charts/account-operator:0.10.13
			// or: oci://ghcr.io/platform-mesh/upstream-images/charts/keycloak:25.2.3
			// repoURL should be: ghcr.io/platform-mesh/helm-charts (without oci:// prefix)
			// Remove oci:// prefix if present
			imageRef := strings.TrimPrefix(imageReference, "oci://")
			// Remove version (everything after ':')
			baseURL := strings.Split(imageRef, ":")[0]
			// Remove chart name (everything after last '/')
			lastSlashIndex := strings.LastIndex(baseURL, "/")
			if lastSlashIndex == -1 {
				log.Error().Str("imageReference", imageReference).Msg("Invalid imageReference format: no '/' found")
				return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("invalid imageReference format"), true, false)
			}
			repoURL = baseURL[:lastSlashIndex]
			// For OCI charts, use version from status.resource.version
			version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
			if err != nil || !found {
				log.Info().Err(err).Msg("Failed to get version from Resource status")
				return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not found in Resource status"), true, false)
			}
			targetRevision = version
			log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", "oci").Msg("Extracted repoURL from imageReference")
		}
	}

	// Get the existing Application to check current values and preserve required fields
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(argocdApplicationGvk)
	err = r.client.Get(ctx, client.ObjectKey{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existingApp)
	if err != nil {
		log.Debug().Err(err).Msg("Application not found, it may not exist yet - skipping update")
		// If Application doesn't exist, we can't update it - this is expected if it hasn't been created by DeploymentSubroutine yet
		// Return success (no error) so we don't retry unnecessarily
		return ctrl.Result{}, nil
	}

	// Check if we need to update (compare current values with desired values)
	currentTargetRevision, _, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "targetRevision")
	currentRepoURL, _, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "repoURL")

	if currentTargetRevision == targetRevision && currentRepoURL == repoURL {
		log.Debug().Str("targetRevision", targetRevision).Str("repoURL", repoURL).Str("application", obj.GetName()).Msg("ArgoCD Application targetRevision and repoURL are already up to date")
		return ctrl.Result{}, nil
	}

	// Use JSON merge patch to update only spec.source.targetRevision and spec.source.repoURL
	// This avoids field manager conflicts with DeploymentSubroutine's Server-Side Apply
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"source": map[string]interface{}{
				"targetRevision": targetRevision,
				"repoURL":        repoURL,
			},
		},
	}

	// Convert patch to JSON
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal patch")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	// Apply JSON merge patch
	err = r.client.Patch(ctx, existingApp, client.RawPatch(types.MergePatchType, patchBytes))
	if err != nil {
		log.Error().Err(err).Msg("Failed to update ArgoCD Application")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	log.Info().Str("targetRevision", targetRevision).Str("repoURL", repoURL).Str("application", obj.GetName()).Str("previousTargetRevision", currentTargetRevision).Str("previousRepoURL", currentRepoURL).Str("isGitChart", fmt.Sprintf("%v", isGitChart)).Msg("Updated ArgoCD Application targetRevision and repoURL")
	return ctrl.Result{}, nil
}

// updateArgoCDApplicationHelmValues updates the Helm values of an ArgoCD Application for image artifacts
// It uses the 'for' annotation to determine the Application name and updates image.tag in Helm values
func (r *ResourceSubroutine) updateArgoCDApplicationHelmValues(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	// Get the Application name from the 'for' annotation
	forVal := getMetadataValue(inst, "for")
	if forVal == "" {
		log.Info().Msg("No 'for' annotation found, using Resource name as Application name")
		forVal = inst.GetName()
	}

	// Parse namespace/name from 'for' annotation (format: "namespace/name" or just "name")
	appName := forVal
	appNamespace := inst.GetNamespace()
	if strings.Contains(forVal, "/") {
		parts := strings.Split(forVal, "/")
		if len(parts) == 2 {
			appNamespace = parts[0]
			appName = parts[1]
		}
	}

	// Get version from Resource status
	version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not found in Resource status"), true, false)
	}

	// Get the existing Application
	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(argocdApplicationGvk)
	err = r.client.Get(ctx, client.ObjectKey{Name: appName, Namespace: appNamespace}, existingApp)
	if err != nil {
		log.Debug().Err(err).Str("application", appName).Str("namespace", appNamespace).Msg("Application not found, it may not exist yet - skipping update")
		// If Application doesn't exist, we can't update it - this is expected if it hasn't been created by DeploymentSubroutine yet
		return ctrl.Result{}, nil
	}

	// Get the path annotation to determine where to set the image tag (default: image.tag)
	pathLabelStr := getMetadataValue(inst, "path")
	updatePath := []string{"image", "tag"}
	if pathLabelStr != "" {
		pathElems := strings.Split(pathLabelStr, ".")
		updatePath = pathElems
	}

	// Get existing Helm values
	existingHelmValuesStr, found, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "helm", "values")
	var helmValues map[string]interface{}
	if found && existingHelmValuesStr != "" {
		// Parse existing Helm values YAML
		if err := yaml.Unmarshal([]byte(existingHelmValuesStr), &helmValues); err != nil {
			log.Warn().Err(err).Msg("Failed to parse existing Helm values, creating new values map")
			helmValues = make(map[string]interface{})
		}
	} else {
		helmValues = make(map[string]interface{})
	}

	// Check if we need to update (compare current image tag with desired version)
	currentImageTag, _ := getNestedString(helmValues, updatePath...)
	if currentImageTag == version {
		log.Debug().Str("version", version).Str("application", appName).Str("path", strings.Join(updatePath, ".")).Msg("ArgoCD Application Helm values image tag is already up to date")
		return ctrl.Result{}, nil
	}

	// Navigate/create the nested structure for the image tag path
	current := helmValues
	for i := 0; i < len(updatePath)-1; i++ {
		key := updatePath[i]
		if val, ok := current[key]; ok {
			if valMap, ok := val.(map[string]interface{}); ok {
				current = valMap
			} else {
				// Path conflict - overwrite with new map
				current[key] = make(map[string]interface{})
				current = current[key].(map[string]interface{})
			}
		} else {
			// Create nested map
			current[key] = make(map[string]interface{})
			current = current[key].(map[string]interface{})
		}
	}

	// Set the image tag value
	lastKey := updatePath[len(updatePath)-1]
	current[lastKey] = version

	// Marshal Helm values back to YAML
	helmValuesYAML, err := yaml.Marshal(helmValues)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal Helm values")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	// Use JSON merge patch to update spec.source.helm.values
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"source": map[string]interface{}{
				"helm": map[string]interface{}{
					"values": string(helmValuesYAML),
				},
			},
		},
	}

	// Convert patch to JSON
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal patch")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	// Apply JSON merge patch
	err = r.client.Patch(ctx, existingApp, client.RawPatch(types.MergePatchType, patchBytes))
	if err != nil {
		log.Error().Err(err).Msg("Failed to update ArgoCD Application")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	log.Info().Str("version", version).Str("application", appName).Str("path", strings.Join(updatePath, ".")).Str("previousImageTag", currentImageTag).Msg("Updated ArgoCD Application Helm values image tag for image artifact")
	return ctrl.Result{}, nil
}

// getNestedString retrieves a nested string value from a map using a path
func getNestedString(m map[string]interface{}, path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	current := m
	for i, key := range path[:len(path)-1] {
		val, ok := current[key]
		if !ok {
			return "", false
		}
		if valMap, ok := val.(map[string]interface{}); ok {
			current = valMap
		} else {
			return "", false
		}
		_ = i // avoid unused variable
	}
	lastKey := path[len(path)-1]
	val, ok := current[lastKey]
	if !ok {
		return "", false
	}
	if strVal, ok := val.(string); ok {
		return strVal, true
	}
	return "", false
}

func (r *ResourceSubroutine) updateHelmRelease(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmReleaseGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
	}

	// Create a minimal patch object with only the field we're updating
	// This ensures Server-Side Apply only tracks ownership of this specific field
	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(helmReleaseGvk)
	patchObj.SetName(obj.GetName())
	patchObj.SetNamespace(obj.GetNamespace())

	// Set only the field we're managing (spec.chart.spec.version)
	if err = unstructured.SetNestedField(patchObj.Object, version, "spec", "chart", "spec", "version"); err != nil {
		log.Error().Err(err).Msg("Failed to set version in HelmRelease spec")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	// Use Server-Side Apply with field manager to update only the specific field
	// This allows Kubernetes to merge with fields managed by other subroutines (e.g., Deployment subroutine)
	err = r.client.Patch(ctx, patchObj, client.Apply,
		client.FieldOwner(resourceFieldManager),
		client.ForceOwnership)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateHelmRepository(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	url, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "helmRepository")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get imageReference from Resource status")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	log.Info().Msg("Processing OCI Chart Resource")
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmRepoGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	// Set desired fields
	if err := unstructured.SetNestedField(obj.Object, url, "spec", "url"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "generic", "spec", "provider"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "5m", "spec", "interval"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Apply using SSA (creates if not exists, updates if exists)
	if err := r.client.Patch(ctx, obj, client.Apply, client.FieldOwner(resourceFieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to apply HelmRepository")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateOciRepo(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
	}
	url, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "imageReference")
	if err != nil || !found || url == "" {
		log.Info().Err(err).Msg("Failed to get imageReference from Resource status")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	url = strings.TrimPrefix(url, "oci://")

	url = "oci://" + url
	url = strings.TrimSuffix(url, ":"+version)

	spec, err := ocm.ParseRef(url)
	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("Failed to parse Resource url")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	url = fmt.Sprintf("%s://%s/%s", spec.Scheme, spec.Host, spec.Repository)

	// Update or create oci repo
	log.Info().Msg("Processing OCI Chart Resource")
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(ociRepoGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	// Set desired fields
	if err := unstructured.SetNestedField(obj.Object, version, "spec", "ref", "tag"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, url, "spec", "url"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "generic", "spec", "provider"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "1m0s", "spec", "interval"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedMap(obj.Object, map[string]interface{}{
		"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
		"operation": "copy",
	}, "spec", "layerSelector"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Apply using SSA (creates if not exists, updates if exists)
	if err := r.client.Patch(ctx, obj, client.Apply, client.FieldOwner(resourceFieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to apply OCIRepository")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateGitRepo(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	commit, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "commit")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
	}

	url, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "repoUrl")
	if err != nil || !found || url == "" {
		log.Info().Err(err).Msg("Failed to get repoUrl from Resource status")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("repoUrl not available in Resource status"), true, false)
	}

	// Update or create git repo
	log.Info().Msg("Processing Git Repository Resource")
	obj := &unstructured.Unstructured{}

	obj.SetGroupVersionKind(gitRepoGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	// Set desired fields
	if err := unstructured.SetNestedField(obj.Object, commit, "spec", "ref", "commit"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, url, "spec", "url"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "1m0s", "spec", "interval"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	if err := unstructured.SetNestedField(obj.Object, "5m", "spec", "timeout"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	// Apply using SSA (creates if not exists, updates if exists)
	if err := r.client.Patch(ctx, obj, client.Apply, client.FieldOwner(resourceFieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to apply GitRepository")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

// getDeploymentTechnologyFromProfile looks up the PlatformMesh instance in the namespace and reads deploymentTechnology from the profile
func (r *ResourceSubroutine) getDeploymentTechnologyFromProfile(ctx context.Context, namespace string, log *logger.Logger) string {
	// Use runtime client for reading PlatformMesh and ConfigMaps (they're in the runtime cluster)
	// Look up PlatformMesh instances in the namespace
	platformMeshList := &v1alpha1.PlatformMeshList{}
	if err := r.clientRuntime.List(ctx, platformMeshList, client.InNamespace(namespace)); err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Msg("Failed to list PlatformMesh instances, trying direct ConfigMap lookup")
		// Fallback: try to read profile ConfigMap directly using common naming pattern
		return r.getDeploymentTechnologyFromConfigMapDirect(ctx, namespace, log)
	}

	if len(platformMeshList.Items) == 0 {
		log.Warn().Str("namespace", namespace).Msg("No PlatformMesh instances found in namespace, trying direct ConfigMap lookup")
		// Fallback: try to read profile ConfigMap directly using common naming pattern
		return r.getDeploymentTechnologyFromConfigMapDirect(ctx, namespace, log)
	}

	// Use the first PlatformMesh instance (typically there's only one per namespace)
	pm := platformMeshList.Items[0]
	log.Info().Str("namespace", namespace).Str("platformMesh", pm.Name).Msg("Found PlatformMesh instance, reading deploymentTechnology from profile")

	// Use the shared helper function to get deploymentTechnology from profile (using runtime client)
	deploymentTech := subroutines.GetDeploymentTechnologyFromProfile(ctx, r.clientRuntime, &pm)
	log.Info().Str("deploymentTechnology", deploymentTech).Str("platformMesh", pm.Name).Str("configMap", pm.Name+"-profile").Msg("Read deploymentTechnology from profile")
	return deploymentTech
}

// getDeploymentTechnologyFromConfigMapDirect tries to read deploymentTechnology directly from profile ConfigMap
// This is a fallback when PlatformMesh lookup fails
func (r *ResourceSubroutine) getDeploymentTechnologyFromConfigMapDirect(ctx context.Context, namespace string, log *logger.Logger) string {
	// Try common ConfigMap name patterns
	configMapNames := []string{"platform-mesh-profile", "platform-mesh-system-profile"}

	for _, cmName := range configMapNames {
		configMap := &corev1.ConfigMap{}
		// Use runtime client for reading ConfigMaps (they're in the runtime cluster)
		if err := r.clientRuntime.Get(ctx, types.NamespacedName{Name: cmName, Namespace: namespace}, configMap); err != nil {
			log.Debug().Err(err).Str("configMap", cmName).Str("namespace", namespace).Msg("ConfigMap not found, trying next")
			continue
		}

		log.Info().Str("configMap", cmName).Str("namespace", namespace).Msg("Found ConfigMap, reading profile.yaml")

		profileYAML, ok := configMap.Data["profile.yaml"]
		if !ok {
			log.Warn().Str("configMap", cmName).Msg("ConfigMap found but profile.yaml key missing")
			continue
		}

		var profile map[string]interface{}
		if err := yaml.Unmarshal([]byte(profileYAML), &profile); err != nil {
			log.Warn().Err(err).Str("configMap", cmName).Msg("Failed to parse profile YAML")
			continue
		}

		log.Debug().Str("configMap", cmName).Msg("Successfully parsed profile YAML, checking for deploymentTechnology")

		// Check infra section first
		if infra, ok := profile["infra"].(map[string]interface{}); ok {
			log.Debug().Str("configMap", cmName).Msg("Found infra section in profile")
			if dt, ok := infra["deploymentTechnology"].(string); ok && dt != "" {
				log.Info().Str("deploymentTechnology", strings.ToLower(dt)).Str("configMap", cmName).Str("section", "infra").Msg("Read deploymentTechnology from profile ConfigMap (direct lookup)")
				return strings.ToLower(dt)
			}
			log.Debug().Str("configMap", cmName).Msg("infra section found but deploymentTechnology not found or empty")
		} else {
			log.Debug().Str("configMap", cmName).Msg("infra section not found in profile")
		}

		// Check components section
		if components, ok := profile["components"].(map[string]interface{}); ok {
			log.Debug().Str("configMap", cmName).Msg("Found components section in profile")
			if dt, ok := components["deploymentTechnology"].(string); ok && dt != "" {
				log.Info().Str("deploymentTechnology", strings.ToLower(dt)).Str("configMap", cmName).Str("section", "components").Msg("Read deploymentTechnology from profile ConfigMap (direct lookup)")
				return strings.ToLower(dt)
			}
			log.Debug().Str("configMap", cmName).Msg("components section found but deploymentTechnology not found or empty")
		} else {
			log.Debug().Str("configMap", cmName).Msg("components section not found in profile")
		}
	}

	log.Warn().Str("namespace", namespace).Msg("Could not find deploymentTechnology in profile ConfigMaps, defaulting to fluxcd")
	return "fluxcd"
}
