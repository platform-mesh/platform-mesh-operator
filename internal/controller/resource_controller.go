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
	"time"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/resource"
)

var (
	resourceReconcilerName = "ResourceReconciler"
)

// ResourceReconciler reconciles a PlatformMesh object
type ResourceReconciler struct {
	lifecycle *controllerruntime.LifecycleManager
}

var gvk = schema.GroupVersionKind{
	Group:   "delivery.ocm.software",
	Version: "v1alpha1",
	Kind:    "Resource",
}

func (r *ResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	return r.lifecycle.Reconcile(ctx, req, obj)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceReconciler) SetupWithManager(mgr ctrl.Manager, cfg *pmconfig.CommonServiceConfig,
	log *logger.Logger, eventPredicates ...predicate.Predicate) error {

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	mgr.GetScheme().AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	mgr.GetScheme().AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})

	builder, err := r.lifecycle.SetupWithManagerBuilder(mgr, cfg.MaxConcurrentReconciles, resourceReconcilerName, obj,
		cfg.DebugLabelValue, log, eventPredicates...)
	if err != nil {
		return err
	}
	return builder.Complete(r)
}

func NewResourceReconciler(log *logger.Logger, mgr ctrl.Manager, cfg *config.OperatorConfig) *ResourceReconciler {
	var subs []subroutine.Subroutine

	subs = append(subs, resource.NewResourceSubroutine(mgr))

	return &ResourceReconciler{
		lifecycle: controllerruntime.NewLifecycleManager(subs, operatorName,
			resourceReconcilerName, mgr.GetClient(), log).WithReadOnly().
			WithStaticThenExponentialRateLimiter(
				ratelimiter.WithRequeueDelay(5*time.Second),
				ratelimiter.WithStaticWindow(10*time.Minute),
				ratelimiter.WithExponentialInitialBackoff(10*time.Second),
				ratelimiter.WithExponentialMaxBackoff(120*time.Second),
			),
	}
}
