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
	kcpHelper KcpHelper,
	cfg *config.OperatorConfig,
	kcpUrl string,
) *WaitSubroutine {
	return &WaitSubroutine{
		client:    client,
		kcpHelper: kcpHelper,
		cfg:       cfg,
		kcpUrl:    kcpUrl,
	}
}

type WaitSubroutine struct {
	client    client.Client
	kcpHelper KcpHelper
	cfg       *config.OperatorConfig
	kcpUrl    string
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
			if resourceType.Name != "" {
				res := &unstructured.Unstructured{}
				res.SetGroupVersionKind(schema.GroupVersionKind{Group: resourceType.Group, Version: version, Kind: resourceType.Kind})
				err := r.client.Get(ctx, client.ObjectKey{Namespace: resourceType.Namespace, Name: resourceType.Name}, res)
				if err != nil {
					log.Info().Msgf("Error getting resource %s/%s: %v", resourceType.Namespace, resourceType.Name, err)
					return ctrl.Result{}, errors.NewOperatorError(err, true, false)
				}
				if !matchesConditionWithStatus(res, string(resourceType.RowConditionType), string(resourceType.ConditionStatus)) {
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
				if !matchesConditionWithStatus(&item, string(resourceType.RowConditionType), string(resourceType.ConditionStatus)) {
					log.Info().Msgf("Resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind())
					return ctrl.Result{}, errors.NewOperatorError(fmt.Errorf("resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind()), true, false)
				}
			}
		}
	}

	// Check if WorkspaceAuthenticationConfiguration audience is still a placeholder
	// If so, trigger a reconcile to ensure all logic is finished
	if err := r.checkWorkspaceAuthConfigAudience(ctx, log); err != nil {
		return ctrl.Result{}, errors.NewOperatorError(err, true, false)
	}

	return ctrl.Result{}, nil
}

func (r *WaitSubroutine) checkWorkspaceAuthConfigAudience(ctx context.Context, log *logger.Logger) error {
	kubeCfg, err := buildKubeconfigFromConfig(r.client, r.cfg, r.kcpUrl)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to build kubeconfig, skipping WorkspaceAuthenticationConfiguration check")
		return nil
	}

	orgsClient, err := r.kcpHelper.NewKcpClient(kubeCfg, "root:orgs")
	if err != nil {
		log.Debug().Err(err).Msg("Failed to create KCP client for root:orgs workspace, skipping")
		return nil
	}

	wac := &unstructured.Unstructured{}
	wac.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "tenancy.kcp.io",
		Version: "v1alpha1",
		Kind:    "WorkspaceAuthenticationConfiguration",
	})

	if err = orgsClient.Get(ctx, types.NamespacedName{Name: "orgs-authentication"}, wac); err != nil {
		log.Debug().Err(err).Msg("Failed to get WorkspaceAuthenticationConfiguration, skipping")
		return nil
	}

	jwtConfigs, found, err := unstructured.NestedSlice(wac.Object, "spec", "jwt")
	if err != nil || !found || len(jwtConfigs) == 0 {
		return nil
	}
	jwt, ok := jwtConfigs[0].(map[string]any)
	if !ok {
		return nil
	}
	issuer, ok := jwt["issuer"].(map[string]any)
	if !ok {
		return nil
	}
	audiences, ok, _ := unstructured.NestedStringSlice(issuer, "audiences")
	if !ok {
		return nil
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
