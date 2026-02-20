package subroutines

import (
	"context"
	"fmt"
	"slices"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

func NewWaitSubroutine(
	client client.Client,
	clientRuntime client.Client,
	cfg *config.OperatorConfig,
	helper KcpHelper,
) *WaitSubroutine {
	return &WaitSubroutine{
		client:        client,
		clientRuntime: clientRuntime,
		cfg:           cfg,
		kcpHelper:     helper,
	}
}

type WaitSubroutine struct {
	client        client.Client // infra cluster — resource readiness checks
	clientRuntime client.Client // runtime cluster — KCP secret access
	cfg           *config.OperatorConfig
	kcpHelper     KcpHelper
}

const (
	WaitSubroutineName = "WaitSubroutine"
)

func (r *WaitSubroutine) Finalize(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *WaitSubroutine) Process(
	ctx context.Context, runtimeObj runtimeobject.RuntimeObject,
) (ctrl.Result, errors.OperatorError) {
	instance := runtimeObj.(*corev1alpha1.PlatformMesh)
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	waitConfig := DEFAULT_WAIT_CONFIG
	if instance.Spec.Wait != nil {
		log.Info().Msg("Using custom WaitConfig")
		waitConfig = *instance.Spec.Wait
	} else {
		log.Info().Msg("No WaitConfig specified, using defaults")
	}

	for _, resourceType := range waitConfig.ResourceTypes {
		log.Info().Msgf("Waiting for resource type: %s", resourceType)

		for _, version := range resourceType.Versions {
			waitList := &unstructured.UnstructuredList{}

			waitList.SetGroupVersionKind(schema.GroupVersionKind{Group: resourceType.Group, Version: version, Kind: resourceType.Kind})

			// Determine which status checking method to use
			useStatusFieldPath := len(resourceType.StatusFieldPath) > 0

			if resourceType.Name != "" {
				res := &unstructured.Unstructured{}
				res.SetGroupVersionKind(schema.GroupVersionKind{Group: resourceType.Group, Version: version, Kind: resourceType.Kind})
				err := r.client.Get(ctx, client.ObjectKey{Namespace: resourceType.Namespace, Name: resourceType.Name}, res)
				if err != nil {
					log.Info().Msgf("Error getting resource %s/%s: %v", resourceType.Namespace, resourceType.Name, err)
					return ctrl.Result{}, errors.NewOperatorError(err, true, false)
				}
				if !checkResourceStatus(res, useStatusFieldPath, resourceType.StatusFieldPath, resourceType.StatusValue, resourceType.ConditionType, string(resourceType.ConditionStatus)) {
					log.Info().Msgf("Resource %s/%s of type %s is not ready yet", resourceType.Namespace, resourceType.Name, res.GetKind())
					return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("resource %s/%s of type %s is not ready yet", resourceType.Namespace, resourceType.Name, res.GetKind()), true, false)
				}
				continue
			}

			// use LabelSelector if no Name is specified
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
				if !checkResourceStatus(&item, useStatusFieldPath, resourceType.StatusFieldPath, resourceType.StatusValue, resourceType.ConditionType, string(resourceType.ConditionStatus)) {
					log.Info().Msgf("Resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind())
					return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind()), true, false)
				}
			}
		}
	}

	// Check if WorkspaceAuthenticationConfiguration audience is still a placeholder
	// If so, trigger a reconcile to ensure all logic is finished
	if err := r.checkWorkspaceAuthConfigAudience(ctx, log, instance); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	return ctrl.Result{}, nil
}

func (r *WaitSubroutine) checkWorkspaceAuthConfigAudience(ctx context.Context, log *logger.Logger, inst *corev1alpha1.PlatformMesh) error {
	kubeCfg, err := buildKubeconfig(ctx, r.clientRuntime, getExternalKcpHost(inst, r.cfg))
	if err != nil {
		return fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	orgsClient, err := r.kcpHelper.NewKcpClient(kubeCfg, "root")
	if err != nil {
		return fmt.Errorf("failed to create KCP client for root workspace: %w", err)
	}

	wac := &unstructured.Unstructured{}
	wac.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "tenancy.kcp.io",
		Version: "v1alpha1",
		Kind:    "WorkspaceAuthenticationConfiguration",
	})

	if err = orgsClient.Get(ctx, types.NamespacedName{Name: "orgs-authentication"}, wac); err != nil {
		return fmt.Errorf("failed to get WorkspaceAuthenticationConfiguration: %w", err)
	}

	jwtConfigs, found, err := unstructured.NestedSlice(wac.Object, "spec", "jwt")
	if err != nil {
		return fmt.Errorf("failed to read spec.jwt from WorkspaceAuthenticationConfiguration: %w", err)
	}
	if !found || len(jwtConfigs) == 0 {
		return fmt.Errorf("WorkspaceAuthenticationConfiguration has no spec.jwt entries")
	}
	jwt, ok := jwtConfigs[0].(map[string]any)
	if !ok {
		return fmt.Errorf("WorkspaceAuthenticationConfiguration spec.jwt[0] has unexpected type")
	}
	issuer, ok := jwt["issuer"].(map[string]any)
	if !ok {
		return fmt.Errorf("WorkspaceAuthenticationConfiguration spec.jwt[0].issuer has unexpected type")
	}
	audiences, ok, _ := unstructured.NestedStringSlice(issuer, "audiences")
	if !ok {
		return fmt.Errorf("WorkspaceAuthenticationConfiguration spec.jwt[0].issuer.audiences not found")
	}

	if slices.Contains(audiences, "<placeholder>") {
		log.Info().Msg("WorkspaceAuthenticationConfiguration audience is still set to <placeholder>, triggering reconcile")
		return errors.New("WorkspaceAuthenticationConfiguration audience is still <placeholder>")
	}

	return nil
}

func (r *WaitSubroutine) Finalizers(instance runtimeobject.RuntimeObject) []string { // coverage-ignore
	return []string{}
}

func (r *WaitSubroutine) GetName() string {
	return WaitSubroutineName
}

// checkResourceStatus checks if a resource matches the expected status.
// If useStatusFieldPath is true, it checks the value at statusFieldPath equals statusValue.
// Otherwise, it checks the conditions array for a matching conditionType and conditionStatus.
func checkResourceStatus(res *unstructured.Unstructured, useStatusFieldPath bool, statusFieldPath []string, statusValue string, conditionType string, conditionStatus string) bool {
	if useStatusFieldPath {
		return matchesStatusFieldValue(res, statusFieldPath, statusValue)
	}
	return matchesConditionWithStatus(res, conditionType, conditionStatus)
}
