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

	pmconfig "github.com/platform-mesh/golang-commons/config"
	"github.com/platform-mesh/golang-commons/controller/filter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/subroutines"
	"github.com/platform-mesh/subroutines/lifecycle"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines/resource"
)

var (
	resourceReconcilerName = "ResourceReconciler"
)

var gvk = schema.GroupVersionKind{
	Group:   "delivery.ocm.software",
	Version: "v1alpha1",
	Kind:    "Resource",
}

// ResourceReconciler reconciles OCM Resource objects
type ResourceReconciler struct {
	lifecycle   *lifecycle.Lifecycle
	rateLimiter workqueue.TypedRateLimiter[mcreconcile.Request]
}

func (r *ResourceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceReconciler) SetupWithManager(mgr mcmanager.Manager, cfg *pmconfig.CommonServiceConfig,
	eventPredicates ...predicate.Predicate) error {

	localMgr := mgr.GetLocalManager()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)

	localMgr.GetScheme().AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	localMgr.GetScheme().AddKnownTypeWithName(gvk.GroupVersion().WithKind(gvk.Kind+"List"), &unstructured.UnstructuredList{})

	opts := controller.TypedOptions[mcreconcile.Request]{
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
		RateLimiter:             r.rateLimiter,
	}
	predicates := append([]predicate.Predicate{filter.DebugResourcesBehaviourPredicate(cfg.DebugLabelValue)}, eventPredicates...)
	return mcbuilder.ControllerManagedBy(mgr).
		Named(resourceReconcilerName).
		For(u).
		WithOptions(opts).
		WithEventFilter(predicate.And(predicates...)).
		Complete(r)
}

// NewResourceReconciler wires the read-only Resource subroutine lifecycle.
func NewResourceReconciler(mgr mcmanager.Manager, cfg *config.OperatorConfig) (*ResourceReconciler, error) {
	localCl := mgr.GetLocalManager().GetClient()
	subs := []subroutines.Subroutine{
		resource.NewResourceSubroutine(localCl),
	}

	rl, err := ratelimiter.NewStaticThenExponentialRateLimiter[mcreconcile.Request](ratelimiter.NewConfig())
	if err != nil {
		return nil, fmt.Errorf("creating rate limiter: %w", err)
	}

	lc := lifecycle.New(mgr, resourceReconcilerName, func() client.Object {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		return u
	}, subs...).WithReadOnly()

	return &ResourceReconciler{
		lifecycle:   lc,
		rateLimiter: rl,
	}, nil
}
