package subroutines

import (
	"context"
	"fmt"

	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/subroutines"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
)

func NewWaitSubroutine(
	client client.Client,
	clientRuntime client.Client,
	cfg *config.OperatorConfig,
	helper KcpHelper,
	kcpUrl string,
) *WaitSubroutine {
	return &WaitSubroutine{
		client:        client,
		clientRuntime: clientRuntime,
		cfg:           cfg,
		kcpHelper:     helper,
		kcpUrl:        kcpUrl,
	}
}

type WaitSubroutine struct {
	client        client.Client // infra cluster — resource readiness checks
	clientRuntime client.Client // runtime cluster — KCP secret access
	cfg           *config.OperatorConfig
	kcpHelper     KcpHelper
	kcpUrl        string
}

const (
	WaitSubroutineName = "WaitSubroutine"
)

func (r *WaitSubroutine) Finalize(
	_ context.Context, _ client.Object,
) (subroutines.Result, error) {
	return subroutines.OK(), nil
}

func (r *WaitSubroutine) Process(
	ctx context.Context, runtimeObj client.Object,
) (subroutines.Result, error) {
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
					return subroutines.StopWithRequeue(DefaultRequeueInterval, "get resource"), nil
				}
				if !checkResourceStatus(res, useStatusFieldPath, resourceType.StatusFieldPath, resourceType.StatusValue, resourceType.ConditionType, string(resourceType.ConditionStatus)) {
					log.Info().Msgf("Resource %s/%s of type %s is not ready yet", resourceType.Namespace, resourceType.Name, res.GetKind())
					return subroutines.StopWithRequeue(DefaultRequeueInterval, fmt.Sprintf("resource %s/%s of type %s is not ready yet", resourceType.Namespace, resourceType.Name, res.GetKind())), nil
				}
				continue
			}

			// use LabelSelector if no Name is specified
			ls, err := v1.LabelSelectorAsSelector(&resourceType.LabelSelector)
			if err != nil {
				log.Info().Msgf("Error converting label selector: %v", err)
				return subroutines.StopWithRequeue(DefaultRequeueInterval, "label selector error"), nil
			}
			err = r.client.List(ctx, waitList, &client.ListOptions{
				Namespace:     resourceType.Namespace,
				LabelSelector: ls,
			})
			if err != nil {
				log.Info().Msgf("Error listing resources: %v", err)
				return subroutines.StopWithRequeue(DefaultRequeueInterval, "list resources error"), nil
			}

			for _, item := range waitList.Items {
				if !checkResourceStatus(&item, useStatusFieldPath, resourceType.StatusFieldPath, resourceType.StatusValue, resourceType.ConditionType, string(resourceType.ConditionStatus)) {
					log.Info().Msgf("Resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind())
					return subroutines.StopWithRequeue(DefaultRequeueInterval, fmt.Sprintf("resource %s/%s of type %s is not ready yet", item.GetNamespace(), item.GetName(), item.GetKind())), nil
				}
			}
		}
	}

	// Check if WorkspaceAuthenticationConfiguration audience is still a placeholder
	// If so, trigger a reconcile to ensure all logic is finished
	if err := r.checkWorkspaceAuthConfigAudience(ctx, log, instance); err != nil {
		return subroutines.StopWithRequeue(DefaultRequeueInterval, err.Error()), nil
	}

	return subroutines.OK(), nil
}

func (r *WaitSubroutine) checkWorkspaceAuthConfigAudience(ctx context.Context, log *logger.Logger, inst *corev1alpha1.PlatformMesh) error {
	kubeCfg, err := buildKubeconfigFromConfig(r.clientRuntime, r.cfg, getExternalKcpHost(inst, r.cfg))
	if err != nil {
		log.Debug().Err(err).Msg("Failed to build kubeconfig, skipping WorkspaceAuthenticationConfiguration check")
		return nil
	}

	orgsClient, err := r.kcpHelper.NewKcpClient(kubeCfg, "root")
	if err != nil {
		log.Debug().Err(err).Msg("Failed to create KCP client for root workspace, skipping")
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

	if len(audiences) == 0 {
		log.Info().Msg("WorkspaceAuthenticationConfiguration audiences is not yet set, triggering reconcile")
		return errors.New("WorkspaceAuthenticationConfiguration audience is not yet set")
	}

	if len(audiences) == 1 {
		if audiences[0] == "<placeholder>" {
			return fmt.Errorf("audiences is set to \"<placeholder>\"")
		}
	}

	return nil
}

func (r *WaitSubroutine) Finalizers(_ client.Object) []string { // coverage-ignore
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
