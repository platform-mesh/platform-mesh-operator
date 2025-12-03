/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1alpha1 "github.com/platform-mesh/platform-mesh-operator/api/v1alpha1"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
)

var (
	pmReconcilerName = "PlatformMeshReconciler"
	operatorName     = "platform-mesh-operator"
)

// PlatformMeshReconciler reconciles a PlatformMesh object
type PlatformMeshReconciler struct {
	lifecycle *controllerruntime.LifecycleManager
}

// +kubebuilder:rbac:groups=core.platform-mesh.io,resources=platformmeshes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.platform-mesh.io,resources=platformmeshes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.platform-mesh.io,resources=platformmeshes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PlatformMesh object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *PlatformMeshReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req, &corev1alpha1.PlatformMesh{})
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlatformMeshReconciler) SetupWithManager(mgr ctrl.Manager, cfg *pmconfig.CommonServiceConfig,
	log *logger.Logger, eventPredicates ...predicate.Predicate) error {
	builder, err := r.lifecycle.SetupWithManagerBuilder(mgr, cfg.MaxConcurrentReconciles, pmReconcilerName, &corev1alpha1.PlatformMesh{},
		cfg.DebugLabelValue, log, eventPredicates...)
	if err != nil {
		return err
	}
	return builder.Complete(r)
}

func NewPlatformMeshReconciler(log *logger.Logger, mgr ctrl.Manager, cfg *config.OperatorConfig, commonCfg *pmconfig.CommonServiceConfig, dir string) *PlatformMeshReconciler {
	//FIXME swith back to the commented out variation when the front-proxy certificate accepts it
	//kcpUrl := fmt.Sprintf("https://%s-front-proxy.%s:%s", cfg.KCP.FrontProxyName, cfg.KCP.Namespace, cfg.KCP.FrontProxyPort)
	kcpUrl := fmt.Sprintf("https://%s-front-proxy:%s", cfg.KCP.FrontProxyName, cfg.KCP.FrontProxyPort)
	if cfg.KCP.Url != "" {
		kcpUrl = cfg.KCP.Url
	}

	if cfg.Deployment.Kubeconfig != "" && cfg.Deployment.Kubeconfig != nil {
		deployClient, err := ctrl.GetConfig()
		if err != nil {
			log.Fatal().Err(err).Msg("unable to get deployment kubeconfig")
		}
	}

	var subs []subroutine.Subroutine
	if cfg.Subroutines.Deployment.Enabled {
		subs = append(subs, subroutines.NewDeploymentSubroutine(deployClient, commonCfg, cfg))
	}
	if cfg.Subroutines.KcpSetup.Enabled {
		subs = append(subs, subroutines.NewKcpsetupSubroutine(mgr.GetClient(), &subroutines.Helper{}, cfg, dir+"/manifests/kcp", kcpUrl))
	}
	if cfg.Subroutines.ProviderSecret.Enabled {
		subs = append(subs, subroutines.NewProviderSecretSubroutine(mgr.GetClient(), &subroutines.Helper{}, subroutines.DefaultHelmGetter{}, kcpUrl))
	}
	if cfg.Subroutines.FeatureToggles.Enabled {
		subs = append(subs, subroutines.NewFeatureToggleSubroutine(mgr.GetClient(), &subroutines.Helper{}, cfg, kcpUrl))
	}
	if cfg.Subroutines.Wait.Enabled {
		subs = append(subs, subroutines.NewWaitSubroutine(mgr.GetClient()))
	}
	return &PlatformMeshReconciler{
		lifecycle: controllerruntime.NewLifecycleManager(subs, operatorName,
			pmReconcilerName, mgr.GetClient(), log).WithConditionManagement().WithStaticThenExponentialRateLimiter(
			ratelimiter.WithRequeueDelay(5*time.Second),
			ratelimiter.WithStaticWindow(10*time.Minute),
			ratelimiter.WithExponentialInitialBackoff(10*time.Second),
			ratelimiter.WithExponentialMaxBackoff(120*time.Second),
		),
	}
}
