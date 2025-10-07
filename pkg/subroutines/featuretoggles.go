package subroutines

import (
	"context"
	"path/filepath"

	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
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
	return BuildKubeconfig(ctx, client, kcpUrl)
}

type FeatureToggleSubroutine struct {
	client             client.Client
	workspaceDirectory string
	kcpUrl             string
	kubeconfigBuilder  KubeconfigBuilder
	kcpHelper          KcpHelper
}

func NewFeatureToggleSubroutine(client client.Client, helper KcpHelper, operatorCfg *config.OperatorConfig, kcpUrl string) *FeatureToggleSubroutine {
	sub := &FeatureToggleSubroutine{
		client:             client,
		workspaceDirectory: filepath.Join(operatorCfg.WorkspaceDir, "/manifests/features/"),
		kcpUrl:             kcpUrl,
		kubeconfigBuilder:  defaultKubeconfigBuilder{},
		kcpHelper:          helper,
	}

	return sub
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

	// Build kcp kubeonfig
	cfg, err := BuildKubeconfig(ctx, r.client, r.kcpUrl)
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
