package controller

import (
	"context"
	"time"

	pmconfig "github.com/platform-mesh/golang-commons/config"
	pmctrl "github.com/platform-mesh/golang-commons/controller/lifecycle/controllerruntime"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/ratelimiter"
	"github.com/platform-mesh/golang-commons/controller/lifecycle/subroutine"
	"github.com/platform-mesh/golang-commons/logger"
	"github.com/platform-mesh/platform-mesh-operator/internal/config"
	"github.com/platform-mesh/platform-mesh-operator/pkg/subroutines"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	keycloakv1alpha1 "github.com/crossplane-contrib/provider-keycloak/apis/realm/v1alpha1"
)

// +kubebuilder:rbac:groups=realm.keycloak.crossplane.io,resources=realms,verbs=get;list;watch
// +kubebuilder:rbac:groups=realm.keycloak.crossplane.io,resources=realms/status,verbs=get
// +kubebuilder:rbac:groups=realm.keycloak.crossplane.io,resources=realms/finalizers,verbs=update

type RealmReconciler struct {
	lifecycle *pmctrl.LifecycleManager
}

func NewRealmReconciler(mgr ctrl.Manager, log *logger.Logger, cfg *config.OperatorConfig) *RealmReconciler {

	clientInfra := mgr.GetClient()
	if cfg.RemoteInfra.Enabled {
		var err error
		clientInfra, _, err = subroutines.GetClientAndRestConfig(cfg.RemoteInfra.Kubeconfig)
		if err != nil {
			log.Fatal().Err(err).Msg("unable to get remote Infra kubeconfig")
			return nil
		}
	}

	return &RealmReconciler{
		lifecycle: pmctrl.NewLifecycleManager(
			[]subroutine.Subroutine{
				subroutines.NewPatchOIDCSubroutine(
					mgr.GetClient(),
					cfg.Subroutines.PatchOIDC.ConfigMapName,
					cfg.Subroutines.PatchOIDC.Namespace,
					cfg.Subroutines.PatchOIDC.BaseDomain,
					cfg.Subroutines.PatchOIDC.DomainCALookup,
				),
			},
			"platform-mesh-operator",
			"RealmReconciler",
			clientInfra,
			log,
		).WithConditionManagement().
			WithStaticThenExponentialRateLimiter(
				ratelimiter.WithRequeueDelay(5*time.Second),
				ratelimiter.WithStaticWindow(10*time.Minute),
				ratelimiter.WithExponentialInitialBackoff(10*time.Second),
				ratelimiter.WithExponentialMaxBackoff(120*time.Second),
			),
	}
}

func (r *RealmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.lifecycle.Reconcile(ctx, req, &keycloakv1alpha1.Realm{})
}

func (r *RealmReconciler) SetupWithManager(mgr ctrl.Manager, cfg *pmconfig.CommonServiceConfig, log *logger.Logger) error {

	return r.lifecycle.WithReadOnly().SetupWithManager(
		mgr,
		cfg.MaxConcurrentReconciles,
		"RealmReconciler",
		&keycloakv1alpha1.Realm{},
		cfg.DebugLabelValue,
		r,
		log,
		[]predicate.Predicate{}...,
	)

}
