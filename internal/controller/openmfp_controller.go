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

	openmfpconfig "github.com/openmfp/golang-commons/config"
	"github.com/openmfp/golang-commons/controller/lifecycle"
	"github.com/openmfp/golang-commons/logger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1alpha1 "github.com/openmfp/openmfp-operator/api/v1alpha1"
	"github.com/openmfp/openmfp-operator/internal/config"
	"github.com/openmfp/openmfp-operator/pkg/subroutines"
)

var (
	openmfpReconcilerName = "OpenMFPReconciler"
	operatorName          = "openmfp-operator"
)

// OpenMFPReconciler reconciles a OpenMFP object
type OpenMFPReconciler struct {
	lifecycle *lifecycle.LifecycleManager
}

// +kubebuilder:rbac:groups=core.openmfp.org,resources=openmfps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.openmfp.org,resources=openmfps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.openmfp.org,resources=openmfps/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OpenMFP object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *OpenMFPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req, &corev1alpha1.OpenMFP{})
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenMFPReconciler) SetupWithManager(mgr ctrl.Manager, cfg *openmfpconfig.CommonServiceConfig,
	log *logger.Logger, eventPredicates ...predicate.Predicate) error {
	builder, err := r.lifecycle.SetupWithManagerBuilder(mgr, cfg.MaxConcurrentReconciles, openmfpReconcilerName, &corev1alpha1.OpenMFP{},
		cfg.DebugLabelValue, log, eventPredicates...)
	if err != nil {
		return err
	}
	return builder.Complete(r)
}

func NewOpenmfpReconciler(log *logger.Logger, mgr ctrl.Manager, cfg *config.OperatorConfig, commonCfg *openmfpconfig.CommonServiceConfig, dir string) *OpenMFPReconciler {
	kcpUrl := "https://kcp-front-proxy.openmfp-system:8443"
	if cfg.KCPUrl != "" {
		kcpUrl = cfg.KCPUrl
	}

	var subs []lifecycle.Subroutine
	if cfg.Subroutines.Deployment.Enabled {
		subs = append(subs, subroutines.NewDeploymentSubroutine(mgr.GetClient(), commonCfg, cfg))
	}
	if cfg.Subroutines.KcpSetup.Enabled {
		subs = append(subs, subroutines.NewKcpsetupSubroutine(mgr.GetClient(), &subroutines.Helper{}, dir+"/manifests/kcp", kcpUrl))
	}
	if cfg.Subroutines.ProviderSecret.Enabled {
		subs = append(subs, subroutines.NewProviderSecretSubroutine(mgr.GetClient(), &subroutines.Helper{}, subroutines.DefaultHelmGetter{}, kcpUrl))
	}
	return &OpenMFPReconciler{
		lifecycle: lifecycle.NewLifecycleManager(log, operatorName,
			openmfpReconcilerName, mgr.GetClient(), subs).WithConditionManagement(),
	}
}
