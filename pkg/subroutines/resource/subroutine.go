package resource

import (
	"context"
	"fmt"
	"strings"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/platform-mesh/platform-mesh-operator/pkg/ocm"
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

type ResourceSubroutine struct {
	client client.Client
}

func NewResourceSubroutine(client client.Client) *ResourceSubroutine {
	return &ResourceSubroutine{client: client}
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

	repo := getMetadataValue(inst, "repo")
	artifact := getMetadataValue(inst, "artifact")

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

	err = r.client.Get(ctx, client.ObjectKeyFromObject(inst), obj)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	// Create a minimal patch object with only the field we're updating
	// This ensures Server-Side Apply only tracks ownership of this specific field
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
		client.FieldOwner("platform-mesh-resource"),
		client.ForceOwnership)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
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
		client.FieldOwner("platform-mesh-resource"),
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
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, obj, func() error {
		err := unstructured.SetNestedField(obj.Object, url, "spec", "url")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj.Object, "generic", "spec", "provider")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj.Object, "5m", "spec", "interval")
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update OCIRepository")
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
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get imageReference from Resource status")
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
	_, err = controllerutil.CreateOrUpdate(ctx, r.client, obj, func() error {
		err := unstructured.SetNestedField(obj.Object, version, "spec", "ref", "tag")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj.Object, url, "spec", "url")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj.Object, "generic", "spec", "provider")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedField(obj.Object, "1m0s", "spec", "interval")
		if err != nil {
			return err
		}
		err = unstructured.SetNestedMap(obj.Object, map[string]interface{}{
			"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
			"operation": "copy",
		}, "spec", "layerSelector")
		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update OCIRepository")
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
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get imageReference from Resource status")
	}

	// Update or create oci repo
	log.Info().Msg("Processing OCI Chart Resource")
	obj := &unstructured.Unstructured{}

	obj.SetGroupVersionKind(gitRepoGvk)
	obj.SetName(inst.GetName())
	obj.SetNamespace(inst.GetNamespace())

	_, err = controllerutil.CreateOrUpdate(ctx, r.client, obj, func() error {

		err := unstructured.SetNestedField(obj.Object, commit, "spec", "ref", "commit")
		if err != nil {
			return err
		}

		err = unstructured.SetNestedField(obj.Object, url, "spec", "url")
		if err != nil {
			return err
		}

		err = unstructured.SetNestedField(obj.Object, "1m0s", "spec", "interval")
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create or update OCIRepository")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}
	return ctrl.Result{}, nil
}
