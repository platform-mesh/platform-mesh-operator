package subroutines

import (
	"context"
	"path/filepath"
	"time"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const FeatureToggleSubroutineName = "FeatureToggleSubroutine"

type KubeconfigBuilder interface {
	Build(ctx context.Context, client client.Client, kcpUrl string) (*rest.Config, error)
}

type defaultKubeconfigBuilder struct{}

func (defaultKubeconfigBuilder) Build(ctx context.Context, client client.Client, kcpUrl string) (*rest.Config, error) {
	return buildKubeconfig(ctx, client, kcpUrl)
}

type FeatureToggleSubroutine struct {
	client             client.Client
	workspaceDirectory string
	kcpUrl             string
	kubeconfigBuilder  KubeconfigBuilder
	kcpHelper          KcpHelper
}

func NewFeatureToggleSubroutine(client client.Client, helper KcpHelper, operatorCfg *config.OperatorConfig, kcpUrl string) *FeatureToggleSubroutine {
	return &FeatureToggleSubroutine{
		client:             client,
		workspaceDirectory: filepath.Join(operatorCfg.WorkspaceDir, "/manifests/features/"),
		kcpUrl:             kcpUrl,
		kubeconfigBuilder:  defaultKubeconfigBuilder{},
		kcpHelper:          helper,
	}
}

func (r *FeatureToggleSubroutine) GetName() string {
	return FeatureToggleSubroutineName
}

func (r *FeatureToggleSubroutine) Finalize(_ context.Context, _ runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	return ctrl.Result{}, nil
}

func (r *FeatureToggleSubroutine) Finalizers() []string { // coverage-ignore
	return []string{}
}

func (r *FeatureToggleSubroutine) Process(ctx context.Context, runtimeObj runtimeobject.RuntimeObject) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())
	operatorCfg := pmconfig.LoadConfigFromContext(ctx).(config.OperatorConfig)

	// Gate on KCP RootShard readiness
	rootShard := &unstructured.Unstructured{}
	rootShard.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "RootShard"})
	if err := r.client.Get(ctx, types.NamespacedName{
		Name:      operatorCfg.KCP.RootShardName,
		Namespace: operatorCfg.KCP.Namespace,
	}, rootShard); err != nil || !MatchesCondition(rootShard, "Available") {
		log.Info().Msg("RootShard is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Gate on KCP FrontProxy readiness
	frontProxy := &unstructured.Unstructured{}
	frontProxy.SetGroupVersionKind(schema.GroupVersionKind{Group: "operator.kcp.io", Version: "v1alpha1", Kind: "FrontProxy"})
	if err := r.client.Get(ctx, types.NamespacedName{
		Name:      operatorCfg.KCP.FrontProxyName,
		Namespace: operatorCfg.KCP.Namespace,
	}, frontProxy); err != nil || !MatchesCondition(frontProxy, "Available") {
		log.Info().Msg("FrontProxy is not ready.. Retry in 5 seconds")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Ensure the KCP admin secret exists before building kubeconfig
	if _, err := GetSecret(r.client, operatorCfg.KCP.ClusterAdminSecretName, operatorCfg.KCP.Namespace); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info().
				Str("secret", operatorCfg.KCP.ClusterAdminSecretName).
				Str("namespace", operatorCfg.KCP.Namespace).
				Msg("KCP admin secret not found yet.. Retry in 5 seconds")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(err, true, true)
	}

	inst := runtimeObj.(*corev1alpha1.PlatformMesh)
	for _, ft := range inst.Spec.FeatureToggles {
		switch ft.Name {
		case "feature-enable-getting-started":
			// Implement the logic to enable the getting started feature
			log.Info().Msg("Getting started feature enabled")
			return r.FeatureGettingStarted(ctx, inst)
		default:
			log.Warn().Str("featureToggle", ft.Name).Msg("Unknown feature toggle")
		}
	}

	return ctrl.Result{}, nil
}

func (r *FeatureToggleSubroutine) FeatureGettingStarted(ctx context.Context, inst *corev1alpha1.PlatformMesh) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	// Implement the logic to enable the getting started feature
	log.Info().Msg("Getting started feature enabled")

	// Build kcp kubeconfig
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}

	dir := r.workspaceDirectory + "/feature-enable-getting-started"

	err = ApplyDirStructure(ctx, dir, "root", cfg, make(map[string]string), inst, r.kcpHelper)
	if err != nil {
		log.Err(err).Msg("Failed to apply dir structure")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to apply dir structure"), true, false)
	}

	return ctrl.Result{}, nil
}
