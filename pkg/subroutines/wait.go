package subroutines

import (
	"context"
	"fmt"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewWaitSubroutine(
	client client.Client,
) *WaitSubroutine {
	sub := &WaitSubroutine{
		client: client,
	}
	return sub
}

type WaitSubroutine struct {
	client client.Client
}

const (
	WaitSubroutineName      = "WaitSubroutine"
	WaitSubroutineFinalizer = "platform-mesh.core.platform-mesh.io/finalizer"
)

func (r *WaitSubroutine) Finalize(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil // TODO: Implement
}

func (r *WaitSubroutine) Process(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	instance := runtimeObj.(*corev1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	if instance.Spec.Wait == nil {
		log.Info().Msg("No WaitConfig specified, using defaults")
		for _, resourceType := range DEFAULT_WAIT_CONFIG.ResourceTypes {
			log.Info().Msgf("Waiting for resource type: %s", resourceType)

			for _, version := range resourceType.APIVersions.Versions {
				waitList := &unstructured.UnstructuredList{}

				waitList.SetGroupVersionKind(schema.GroupVersionKind{Group: resourceType.Group, Version: version, Kind: resourceType.Kind})
				ls, err := v1.LabelSelectorAsSelector(&resourceType.LabelSelector)
				if err != nil {
					log.Info().Msgf("Error converting label selector: %v", err)
					return ctrl.Result{}, errors.NewOperatorError(err, true, false)
				}
				err = r.client.List(ctx, waitList, &client.ListOptions{
					Namespace:     resourceType.Namespace,
					LabelSelector: ls,
				})
				if err != nil {
					log.Info().Msgf("Error listing resources: %v", err)
					return ctrl.Result{}, errors.NewOperatorError(err, true, false)
				}

				for _, item := range waitList.Items {
					if !MatchesCondition(&item, string(resourceType.RowConditionType)) {
						log.Info().Msgf("Resource %s/%s of type %s is not ready yet, requeuing", item.GetNamespace(), item.GetName(), waitList.GetKind())
						return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("resource %s/%s of type %s is not ready yet, requeuing", item.GetNamespace(), item.GetName(), item.GetKind()), true, false)
					}
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *WaitSubroutine) Finalizers() []string { // coverage-ignore
	return []string{WaitSubroutineFinalizer}
}

func (r *WaitSubroutine) GetName() string {
	return WaitSubroutineName
}
