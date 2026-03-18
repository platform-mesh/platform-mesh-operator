package resource

import (
	"context"
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
	client            client.Client // infra client for creating FluxCD resources
	clientRuntime     client.Client // runtime client for reading profile ConfigMaps
	cfg               *config.OperatorConfig
	imageVersionStore *subroutines.ImageVersionStore
}

func NewResourceSubroutine(client client.Client, cfg *config.OperatorConfig, imageVersionStore *subroutines.ImageVersionStore) *ResourceSubroutine {
	return &ResourceSubroutine{client: client, clientRuntime: client, cfg: cfg, imageVersionStore: imageVersionStore}
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

	deploymentTech, err := r.getDeploymentTechnologyFromProfile(ctx, inst.GetNamespace(), log)
	if err != nil {
		log.Error().Err(err).Str("namespace", inst.GetNamespace()).Msg("Failed to determine deploymentTechnology from profile")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	if deploymentTech == "" {
		log.Warn().Str("namespace", inst.GetNamespace()).Msg("deploymentTechnology not configured in profile, defaulting to fluxcd")
		deploymentTech = "fluxcd"
	}
	log.Debug().Str("deploymentTechnology", deploymentTech).Str("resource", inst.GetName()).Str("namespace", inst.GetNamespace()).Msg("Checking deployment technology for Resource")

	repo := getMetadataValue(inst, "repo")
	artifact := getMetadataValue(inst, "artifact")

	if deploymentTech == "argocd" {
		// argocd logic
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
			log.Warn().Str("artifact", artifact).Msg("ArgoCD is enabled but artifact is not 'chart' or 'image', skipping")
		}
		return ctrl.Result{}, nil
	}

	// fluxcd logic
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
	name, namespace := parseNamespacedName(getMetadataValue(inst, "for"), inst.GetName(), inst.GetNamespace())
	updatePath := append([]string{"spec", "values"}, parsePath(getMetadataValue(inst, "path"), "image.tag")...)
	versionPath := parsePath(getMetadataValue(inst, "version-path"), "status.resource.version")

	version, found, _ := unstructured.NestedString(inst.Object, versionPath...)
	if !found || version == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not available at path %v", versionPath), true, false)
	}

	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(helmReleaseGvk)
	patchObj.SetName(name)
	patchObj.SetNamespace(namespace)

	if err := unstructured.SetNestedField(patchObj.Object, version, updatePath...); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	fieldManager := fmt.Sprintf("%s-%s", resourceFieldManager, inst.GetName())
	if err := r.client.Patch(ctx, patchObj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to update HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	helmValuesPath := strings.Join(updatePath[2:], ".")
	r.storeImageVersion(namespace, name, helmValuesPath, version)
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateArgoCDApplication(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	repoURL, targetRevision, chartType, err := r.resolveArgoCDSource(inst)
	if err != nil {
		log.Info().Err(err).Msg("Failed to resolve ArgoCD source")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	log.Debug().Str("repoURL", repoURL).Str("targetRevision", targetRevision).Str("type", chartType).Msg("Resolved ArgoCD source")

	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(argocdApplicationGvk)
	if err := r.client.Get(ctx, client.ObjectKey{Name: inst.GetName(), Namespace: inst.GetNamespace()}, existingApp); err != nil {
		log.Info().Err(err).Msg("Application not found, waiting for DeploymentSubroutine to create it")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("application %s/%s not found", inst.GetNamespace(), inst.GetName()), true, false)
	}

	currentRevision, _, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "targetRevision")
	currentRepoURL, _, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "repoURL")
	if currentRevision == targetRevision && currentRepoURL == repoURL {
		return ctrl.Result{}, nil
	}

	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(argocdApplicationGvk)
	patchObj.SetName(inst.GetName())
	patchObj.SetNamespace(inst.GetNamespace())
	if err := unstructured.SetNestedField(patchObj.Object, targetRevision, "spec", "source", "targetRevision"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	if err := unstructured.SetNestedField(patchObj.Object, repoURL, "spec", "source", "repoURL"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	fieldManager := fmt.Sprintf("%s-%s", resourceFieldManager, inst.GetName())
	if err := r.client.Patch(ctx, patchObj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to update ArgoCD Application")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	log.Info().
		Str("application", inst.GetName()).
		Str("repoURL", repoURL).
		Str("targetRevision", targetRevision).
		Str("previousRevision", currentRevision).
		Msg("Updated ArgoCD Application")
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) resolveArgoCDSource(inst *unstructured.Unstructured) (repoURL, targetRevision, chartType string, err error) {
	getString := func(path ...string) string {
		val, _, _ := unstructured.NestedString(inst.Object, path...)
		return val
	}

	// Helm repository
	if helmRepo := getString("status", "resource", "access", "helmRepository"); helmRepo != "" {
		version := getString("status", "resource", "version")
		if version == "" {
			return "", "", "", fmt.Errorf("version not found for helm chart")
		}
		return helmRepo, version, "helm", nil
	}

	// Git repository
	if gitRepo := getString("status", "resource", "access", "repoUrl"); gitRepo != "" {
		revision := firstNonEmpty(
			getString("status", "resource", "access", "ref"),
			getString("status", "component", "version"),
			getString("status", "resource", "version"),
			getString("status", "resource", "access", "commit"),
		)
		if revision == "" {
			return "", "", "", fmt.Errorf("no ref, version, or commit found for git chart")
		}
		return gitRepo, revision, "git", nil
	}

	// OCI image reference
	imageRef := getString("status", "resource", "access", "imageReference")
	if imageRef == "" {
		imageRef = getString("status", "resource", "imageReference")
	}
	if imageRef == "" {
		return "", "", "", fmt.Errorf("no helmRepository, repoUrl, or imageReference found")
	}

	repoURL, err = extractOCIRepoURL(imageRef)
	if err != nil {
		return "", "", "", err
	}

	version := getString("status", "resource", "version")
	if version == "" {
		return "", "", "", fmt.Errorf("version not found for OCI chart")
	}
	return repoURL, version, "oci", nil
}

func extractOCIRepoURL(imageRef string) (string, error) {
	imageRef = strings.TrimPrefix(imageRef, "oci://")
	baseURL := strings.Split(imageRef, ":")[0]
	lastSlash := strings.LastIndex(baseURL, "/")
	if lastSlash == -1 {
		return "", fmt.Errorf("invalid imageReference format: %s", imageRef)
	}
	return baseURL[:lastSlash], nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (r *ResourceSubroutine) updateArgoCDApplicationHelmValues(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	appName, appNamespace := parseNamespacedName(getMetadataValue(inst, "for"), inst.GetName(), inst.GetNamespace())
	updatePath := parsePath(getMetadataValue(inst, "path"), "image.tag")
	pathStr := strings.Join(updatePath, ".")

	version, found, _ := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if !found {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not found"), true, false)
	}

	existingApp := &unstructured.Unstructured{}
	existingApp.SetGroupVersionKind(argocdApplicationGvk)
	if err := r.client.Get(ctx, client.ObjectKey{Name: appName, Namespace: appNamespace}, existingApp); err != nil {
		log.Info().Err(err).Str("application", appName).Msg("Application not found, waiting for creation")
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("application %s/%s not found", appNamespace, appName), true, false)
	}

	existingValues, _, _ := unstructured.NestedString(existingApp.Object, "spec", "source", "helm", "values")
	currentTag := getValueFromYAML(existingValues, updatePath)
	if currentTag == version {
		r.storeImageVersion(appNamespace, appName, pathStr, version)
		return ctrl.Result{}, nil
	}

	updatedValues, err := subroutines.SetHelmValues(existingValues, []subroutines.ImageVersion{{Path: pathStr, Version: version}})
	if err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(argocdApplicationGvk)
	patchObj.SetName(appName)
	patchObj.SetNamespace(appNamespace)
	if err := unstructured.SetNestedField(patchObj.Object, updatedValues, "spec", "source", "helm", "values"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	fieldManager := fmt.Sprintf("%s-%s", resourceFieldManager, inst.GetName())
	if err := r.client.Patch(ctx, patchObj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	log.Info().Str("application", appName).Str("path", pathStr).Str("version", version).Msg("Updated ArgoCD Application helm values")
	r.storeImageVersion(appNamespace, appName, pathStr, version)
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) storeImageVersion(namespace, name, path, version string) {
	if r.imageVersionStore != nil {
		r.imageVersionStore.Set(namespace, name, path, version)
	}
}

func parseNamespacedName(forVal, defaultName, defaultNamespace string) (name, namespace string) {
	if forVal == "" {
		return defaultName, defaultNamespace
	}
	if parts := strings.Split(forVal, "/"); len(parts) == 2 {
		return parts[1], parts[0]
	}
	return forVal, defaultNamespace
}

func parsePath(pathStr, defaultPath string) []string {
	if pathStr == "" {
		return strings.Split(defaultPath, ".")
	}
	return strings.Split(pathStr, ".")
}

func getValueFromYAML(yamlStr string, path []string) string {
	if yamlStr == "" {
		return ""
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlStr), &m); err != nil {
		return ""
	}
	val, _ := getNestedString(m, path...)
	return val
}

// getNestedString retrieves a nested string value from a map using a path
func getNestedString(m map[string]interface{}, path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	current := m
	for _, key := range path[:len(path)-1] {
		val, ok := current[key]
		if !ok {
			return "", false
		}
		if valMap, ok := val.(map[string]interface{}); ok {
			current = valMap
		} else {
			return "", false
		}
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
	version, found, _ := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if !found || version == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("version not available"), true, false)
	}

	patchObj := &unstructured.Unstructured{}
	patchObj.SetGroupVersionKind(helmReleaseGvk)
	patchObj.SetName(inst.GetName())
	patchObj.SetNamespace(inst.GetNamespace())

	if err := unstructured.SetNestedField(patchObj.Object, version, "spec", "chart", "spec", "version"); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	fieldManager := fmt.Sprintf("%s-%s", resourceFieldManager, inst.GetName())
	if err := r.client.Patch(ctx, patchObj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to update HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateHelmRepository(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	url, found, _ := unstructured.NestedString(inst.Object, "status", "resource", "access", "helmRepository")
	if !found || url == "" {
		return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("helmRepository not available"), true, false)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(helmRepoGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())
	_ = unstructured.SetNestedField(obj.Object, url, "spec", "url")
	_ = unstructured.SetNestedField(obj.Object, "generic", "spec", "provider")
	_ = unstructured.SetNestedField(obj.Object, "5m", "spec", "interval")

	if err := r.client.Patch(ctx, obj, client.Apply, client.FieldOwner(resourceFieldManager), client.ForceOwnership); err != nil {
		log.Error().Err(err).Msg("Failed to apply HelmRepository")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceSubroutine) updateOciRepo(ctx context.Context, inst *unstructured.Unstructured, log *logger.Logger) (ctrl.Result, errors.OperatorError) {
	version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if err != nil || !found || version == "" {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
		if err == nil {
			err = fmt.Errorf("version not available in Resource status")
		}
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}
	url, found, err := unstructured.NestedString(inst.Object, "status", "resource", "access", "imageReference")
	if err != nil || !found || url == "" {
		log.Info().Err(err).Msg("Failed to get imageReference from Resource status")
		if err == nil {
			err = fmt.Errorf("imageReference not available in Resource status")
		}
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
	if err != nil || !found || commit == "" {
		log.Info().Err(err).Msg("Failed to get commit from Resource status")
		if err == nil {
			err = fmt.Errorf("commit not available in Resource status")
		}
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
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

func (r *ResourceSubroutine) getDeploymentTechnologyFromProfile(ctx context.Context, namespace string, log *logger.Logger) (string, error) {
	platformMeshList := &v1alpha1.PlatformMeshList{}
	if err := r.clientRuntime.List(ctx, platformMeshList, client.InNamespace(namespace)); err != nil {
		log.Warn().Err(err).Str("namespace", namespace).Msg("Failed to list PlatformMesh instances, trying direct ConfigMap lookup")
		return r.getDeploymentTechnologyFromConfigMapDirect(ctx, namespace, log)
	}

	if len(platformMeshList.Items) == 0 {
		log.Warn().Str("namespace", namespace).Msg("No PlatformMesh instances found in namespace, trying direct ConfigMap lookup")
		return r.getDeploymentTechnologyFromConfigMapDirect(ctx, namespace, log)
	}

	if len(platformMeshList.Items) > 1 {
		names := make([]string, len(platformMeshList.Items))
		for i, item := range platformMeshList.Items {
			names[i] = item.Name
		}
		log.Warn().Strs("platformMeshInstances", names).Str("namespace", namespace).Msg("Multiple PlatformMesh instances found in namespace, using first one")
	}

	pm := platformMeshList.Items[0]
	log.Info().Str("namespace", namespace).Str("platformMesh", pm.Name).Msg("Found PlatformMesh instance, reading deploymentTechnology from profile")

	deploymentTech, err := subroutines.GetDeploymentTechnologyFromProfile(ctx, r.clientRuntime, &pm)
	if err != nil {
		log.Error().Err(err).Str("platformMesh", pm.Name).Msg("Failed to read deploymentTechnology from profile")
		return "", err
	}
	log.Info().Str("deploymentTechnology", deploymentTech).Str("platformMesh", pm.Name).Str("configMap", pm.Name+"-profile").Msg("Read deploymentTechnology from profile")
	return deploymentTech, nil
}

func (r *ResourceSubroutine) getDeploymentTechnologyFromConfigMapDirect(ctx context.Context, namespace string, log *logger.Logger) (string, error) {
	configMapNames := []string{"platform-mesh-profile", "platform-mesh-system-profile"}

	for _, cmName := range configMapNames {
		configMap := &corev1.ConfigMap{}
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
			return "", fmt.Errorf("failed to parse profile YAML from ConfigMap %s/%s: %w", namespace, cmName, err)
		}

		// Check infra section first
		if infra, ok := profile["infra"].(map[string]interface{}); ok {
			if dt, ok := infra["deploymentTechnology"].(string); ok && dt != "" {
				log.Info().Str("deploymentTechnology", strings.ToLower(dt)).Str("configMap", cmName).Str("section", "infra").Msg("Read deploymentTechnology from profile ConfigMap (direct lookup)")
				return strings.ToLower(dt), nil
			}
		}

		// Check components section
		if components, ok := profile["components"].(map[string]interface{}); ok {
			if dt, ok := components["deploymentTechnology"].(string); ok && dt != "" {
				log.Info().Str("deploymentTechnology", strings.ToLower(dt)).Str("configMap", cmName).Str("section", "components").Msg("Read deploymentTechnology from profile ConfigMap (direct lookup)")
				return strings.ToLower(dt), nil
			}
		}

		log.Debug().Str("configMap", cmName).Msg("Profile ConfigMap found but deploymentTechnology not configured")
		return "", nil
	}

	return "", fmt.Errorf("no profile ConfigMap found in namespace %s (tried: %v)", namespace, configMapNames)
}
