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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

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
	mgr manager.Manager
}

func NewResourceSubroutine(mgr manager.Manager) *ResourceSubroutine {
	return &ResourceSubroutine{mgr: mgr}
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

func (r *ResourceSubroutine) Process(ctx context.Context, runtimeObj runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	inst := runtimeObj.(*unstructured.Unstructured)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("name", r.GetName())

	annotations := getAnnotations(inst)
	repo := annotations["repo"]
	artifact := annotations["artifact"]

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

	annotations := getAnnotations(inst)
	forVal := annotations["for"]
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

	pathLabel := annotations["path"]
	updatePath := []string{"spec", "values", "image", "tag"}
	if pathLabel != "" {
		pathElems := strings.Split(pathLabel, ".")
		updatePath = []string{"spec", "values"}
		updatePath = append(updatePath, pathElems...)
	}

	version, found, err := unstructured.NestedString(inst.Object, "status", "resource", "version")
	if err != nil || !found {
		log.Info().Err(err).Msg("Failed to get version from Resource status")
	}

	err = r.mgr.GetClient().Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, obj)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	err = unstructured.SetNestedField(obj.Object, version, updatePath...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to set version in HelmRelease spec")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	err = r.mgr.GetClient().Update(ctx, obj)
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

	err = r.mgr.GetClient().Get(ctx, client.ObjectKeyFromObject(inst), obj)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get HelmRelease")
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	err = unstructured.SetNestedField(obj.Object, version, "spec", "chart", "spec", "version")
	if err != nil {
		log.Error().Err(err).Msg("Failed to set version in HelmRelease spec")
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	err = r.mgr.GetClient().Update(ctx, obj)
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
	_, err = controllerutil.CreateOrUpdate(ctx, r.mgr.GetClient(), obj, func() error {
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
	_, err = controllerutil.CreateOrUpdate(ctx, r.mgr.GetClient(), obj, func() error {
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

	_, err = controllerutil.CreateOrUpdate(ctx, r.mgr.GetClient(), obj, func() error {

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
