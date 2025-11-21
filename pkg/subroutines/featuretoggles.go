package subroutines

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"time"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/runtimeobject"
	"github.com/platform-mesh/golang-commons/errors"
	"github.com/platform-mesh/golang-commons/logger"
	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	inst := runtimeObj.(*corev1alpha1.PlatformMesh)
	for _, ft := range inst.Spec.FeatureToggles {
		switch ft.Name {
		case "feature-enable-getting-started":
			// Implement the logic to enable the getting started feature
			_, opErr := r.applyKcpManifests(ctx, inst, operatorCfg, "/feature-enable-getting-started")
			if opErr != nil {
				log.Error().Err(opErr.Err()).Msg("Failed to apply getting started manifests")
				return ctrl.Result{}, opErr
			}
			log.Info().Msg("Enabled 'Getting started configuration' feature")
		case "feature-enable-iam":
			// Implement the logic to enable the IAM feature
			_, opErr := r.applyKcpManifests(ctx, inst, operatorCfg, "/feature-enable-iam")
			if opErr != nil {
				log.Error().Err(opErr.Err()).Msg("Failed to apply IAM manifests")
				return ctrl.Result{}, opErr
			}
			log.Info().Msg("Enabled 'IAM configuration' feature")
		case "feature-enable-marketplace":
			// Implement the logic to enable the marketplace feature
			_, opErr := r.applyKcpManifests(ctx, inst, operatorCfg, "/feature-enable-marketplace")
			if opErr != nil {
				log.Error().Err(opErr.Err()).Msg("Failed to apply marketplace manifests")
				return ctrl.Result{}, opErr
			}
			log.Info().Msg("Enabled 'Marketplace configuration' feature")
		default:
			log.Warn().Str("featureToggle", ft.Name).Msg("Unknown feature toggle")
		}
	}

	return ctrl.Result{}, nil
}

func (r *FeatureToggleSubroutine) applyKcpManifests(
	ctx context.Context,
	inst *corev1alpha1.PlatformMesh,
	operatorCfg config.OperatorConfig,
	kcpDir string,
) (ctrl.Result, errors.OperatorError) {
	log := logger.LoadLoggerFromContext(ctx).ChildLogger("subroutine", r.GetName())

	// Implement the logic to enable the getting started feature
	log.Info().Str("Directory", kcpDir).Msg("Applying KCP manifests for feature toggle")

	// Ensure the KCP admin secret exists before building kubeconfig
	secret := &corev1.Secret{}
	if err := r.client.Get(ctx, types.NamespacedName{
		Name:      operatorCfg.KCP.ClusterAdminSecretName,
		Namespace: operatorCfg.KCP.Namespace,
	}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info().
				Str("secret", operatorCfg.KCP.ClusterAdminSecretName).
				Str("namespace", operatorCfg.KCP.Namespace).
				Msg("KCP admin secret not found yet.. Retry in 5 seconds")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to get secret"), true, true)
	}

	// Build kcp kubeconfig
	cfg, err := buildKubeconfig(ctx, r.client, r.kcpUrl)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build kubeconfig")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to build kubeconfig"), true, false)
	}

	dir := r.workspaceDirectory + kcpDir

	baseDomain, baseDomainPort, port, protocol := baseDomainPortProtocol(inst)
	tplValues := map[string]string{
		"iamWebhookCA":   base64.StdEncoding.EncodeToString(secret.Data["ca.crt"]),
		"baseDomain":     baseDomain,
		"protocol":       protocol,
		"port":           fmt.Sprintf("%d", port),
		"baseDomainPort": baseDomainPort,
	}

	err = ApplyDirStructure(ctx, dir, "root", cfg, tplValues, inst, r.kcpHelper)
	if err != nil {
		log.Err(err).Msg("Failed to apply dir structure")
		return ctrl.Result{}, errors.NewOperatorError(errors.Wrap(err, "Failed to apply dir structure"), true, false)
	}

	return ctrl.Result{}, nil
}
